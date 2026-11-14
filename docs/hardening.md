# Security

This page covers logins, the first admin account, reverse proxies, passkeys, API keys and what the subflux image contains, for readers who expose subflux beyond one machine or audit what they run.

## Logins

Login is on by default. Users sign in with a password, a passkey or OIDC single sign-on, and an admin can create API keys for scripts.

Sign-in attempts are rate-limited. One client address may fail 10 times in 15 minutes, an IPv6 client counting as its whole `/64` network, and one account 100 times an hour from browsers that have not signed in to it before, before further attempts are refused until the oldest failure ages out. A password check uses 19 MiB of memory, and subflux runs at most four at once, so a burst of sign-ins cannot exhaust the host's memory: a sign-in beyond that answers `503 auth_busy`, counts as no failure and can be tried again at once.

`auth.disable_auth: true` turns login off and treats every request as an admin. subflux reads it only at start, so set it in `config.yaml` and restart. It then logs a warning and shows an alert on the web page until you remove the setting. When subflux refuses the sign-in settings at start, login stays on whatever `disable_auth` says, and a `restart_pending` problem says that the `disable_auth: true` in `config.yaml` turns login off at a restart that accepts them. If the auth section does not load at all, that problem shows once a save repairs it.

## The first admin account

Until the first account exists, subflux writes a one-time setup token to its log at every start, valid for 24 hours:

```text
accounts: no account exists yet; open subflux to set one up. Setting up from a hostname? add it to server.allowed_hosts, or paste this token
```

Run `docker logs subflux` to read it, or `docker exec subflux subflux setup-token` for a fresh one. The setup page needs no token when both of these hold:

- the browser connects to subflux directly, not through a proxy, from a loopback or private address (`127.0.0.0/8`, `::1`, `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`, `fc00::/7`) that is not one of the host's own forwarders, such as Docker's bridge gateway;
- the address in the browser's bar is an IP address, `localhost`, or a host name listed in `server.allowed_hosts`.

A request through a proxy listed in `server.trusted_proxies` always needs the token, whatever client address the proxy forwards, since subflux cannot check what sits in front of that proxy: if you first open subflux through your own reverse proxy, paste the token once. A request from an address under which the host relays other machines' connections, such as a container runtime's gateway, a tunnel's endpoint or a router, needs the token too, and so does every request while subflux cannot read the host's routes.

So `http://192.0.2.10:8374` from your network needs no token, while `http://nas.lan:8374` asks for it until you add `nas.lan` to `server.allowed_hosts` or paste the token. This stops a malicious web page from creating the admin account through DNS rebinding: such a page makes its own host name resolve to subflux's address, so its requests carry that name, which is not on your list. While the `server` section of `config.yaml` does not load, every client needs the token.

## What subflux checks

- Each subtitle site's address is checked before every fetch, so a site cannot make subflux request a service on your network.
- Saved site passwords and API keys are left out of every settings answer to the browser. An empty secret field on save keeps the stored value.
- Archive extraction refuses archives that expand far beyond their size.
- Every answer from an outside service is size-capped and checked before use.

A saved secret stays with its entry when its address changes: an admin who points a Sonarr, Radarr or site entry at another URL sends the stored API key or password there. Admin access therefore controls every credential subflux holds, so give the admin role only to people you would trust with them.

## Running behind a reverse proxy

Behind a [reverse proxy](https://github.com/cplieger/docs/blob/main/docs/reverse-proxy.md), subflux sees the proxy's address and not the browser's. Set `server.trusted_proxies` to the proxy's address or address range. subflux then reads the real client address from a trusted `X-Forwarded-For` header. It uses that address for the security log, the login rate limit, the session record and the access log, as in this example:

```yaml
server:
  trusted_proxies:
    - 192.0.2.0/24
    - 198.51.100.7
```

Entries are single addresses or CIDR ranges. `X-Forwarded-For` is read only when the direct peer is in one of them, from right to left, so a client cannot fake its address. An invalid entry is refused when the settings load. Leave `server.trusted_proxies` empty, the default, when nothing sits in front of subflux. The direct peer is then used and `X-Forwarded-For` is ignored.

List the proxy before you expose subflux. A proxy that is not listed and adds no `X-Forwarded-For` header looks like a browser connecting directly from its own address, which is usually private, so subflux treats a visitor from the internet as one on your network: the setup page then needs no token. A listed proxy never makes a client local, and neither does a proxy that is not listed but adds the header, as Caddy does by default, since a client sending forwarding headers it is not trusted for is never local.

## Blocking DNS rebinding

`server.allowed_hosts` lists the exact host names or addresses subflux answers for, each with an optional port. A request whose `Host` header is not on the list is refused with 403 before it reaches any page:

```yaml
server:
  allowed_hosts:
    - subflux.example.com
    - 192.0.2.5
```

This closes a gap the cross-origin check leaves open. In a DNS rebinding attack, a malicious page makes its own host name resolve to subflux's address. The browser's request then carries the attacker's name in both `Origin` and `Host`, they agree, and the cross-origin check lets it through. Only an exact `Host` check stops it. A request from the container itself to a loopback address, such as the healthcheck, always passes. Leave `server.allowed_hosts` empty, the default, to accept any `Host`. A `server.allowed_hosts` entry that does not parse is refused when the settings load. Until a save fixes it, only the entries that did parse are admitted, and a list with none admits no `Host`, so a typo never opens the list to every host.

## Passkeys

`auth.passkey_origins` lists the addresses passkeys sign in from, and an empty list, the default, turns passkeys off:

```yaml
auth:
  passkey_origins:
    - https://subflux.example.com
```

Each entry is an `https` address with a host name and no path, or `http://localhost` with an optional port, which the WebAuthn specification treats as trustworthy. A passkey belongs to one host name, its relying party ID: the listed host that every other listed host equals or sits under, so `https://subflux.example.com` and `https://tv.subflux.example.com` together use `subflux.example.com`, and a list with no such host is refused. subflux accepts a passkey only from an address on the list, so a sibling host such as `https://other.example.com` cannot use it.

A deployment reached by IP address cannot use passkeys, because an IP address is not a legal passkey host. With `server.allowed_hosts` set, every listed origin's host must be on it too.

Changing the list so the relying party ID moves makes the existing passkeys stop working, since each stays bound to the old ID. A save that would leave a user with no way to sign in, such as a user with only passkeys, is refused, and the same holds for turning off password sign-in or OIDC. Add a password or a passkey under the new address first.

## API keys and their permissions

An admin creates API keys for scripts and agents. A key carries the scopes ticked when it was created: `read`, `operate` and `admin`, or none for its owner's full access. Two permissions are never implied, by `admin` or by full access, and must be ticked one by one:

- `manage_users` creates, invites, resets, disables and deletes regular accounts, and never touches an admin account. The accounts a key creates or takes over stay after the key is revoked, and Settings lists the users each key acted on, so treat it as the power to create and take over regular accounts.
- `create_keys` creates further keys, each with a subset of its own scopes and an expiry no later than its own. Revoking or expiring a key revokes every key it created, and every invite or recovery link it issued.

## Running subflux on the internet

subflux is built for your own network. If you expose it to the internet anyway:

- **Put it behind a reverse proxy that serves HTTPS only, and set HSTS there.** subflux serves plain HTTP and does not send `Strict-Transport-Security` itself, so set that header at the proxy and redirect plain HTTP to HTTPS.
- **List the proxy in `server.trusted_proxies` before exposing it**, and set `server.allowed_hosts` to the public host name.
- **Create the admin account before you expose it.** Until one exists, a visitor with the setup token from the log can create it, and the token never leaves the log.
- **Prefer OIDC, or a forward-auth gate at the proxy**, such as Authelia or Authentik, so a sign-in passes your identity provider's own protections first. With `auth.oidc_auto_provision`, only people whose `groups` claim holds one of `auth.oidc_allowed_groups` get an account; keep that list short. A forward-auth gate must let Prometheus reach `/metrics` another way, such as on the local network.
- **Use passkeys** for the accounts that have no OIDC, with `auth.passkey_origins` set to the public address.
- **Shorten the session timeouts**, such as `auth.session_idle_timeout: 1h` and `auth.session_absolute_timeout: 24h`, so a stolen session cookie stops working sooner.
- **Know what answers without a login.** `/api/health`, `/metrics`, the web page's own files and the sign-in endpoints under `/api/auth` (setup, password and passkey sign-in, sign-out, the OIDC start and callback, recovery and invite links) are public. Block `/metrics` at the proxy unless Prometheus scrapes it from outside. The rate limits above apply to the sign-in endpoints.

subflux watches for exposure it can see. When a client with a public address reaches it over plain HTTP, through a proxy `server.trusted_proxies` does not list, or with that list empty, admins see a warning in Settings, under Access, for 24 hours after the last such request, and subflux logs it at most once an hour per reason. A save that changes the `server` section clears the warning, so it returns only if the cause remains.

## What the image contains

The image has no shell and no package manager. It runs as the user in your `compose.yaml`, or as `nonroot` (UID 65532) without one. Every pin below is tracked by automated pull requests. The two media libraries are built from source in the image, because no distribution ships them in the shape subflux needs.

| Component | Source |
| --- | --- |
| Base image | `gcr.io/distroless/static-debian13:nonroot`, pinned by digest |
| FFmpeg and ffprobe | built from source at a pinned release tag, statically linked, without network support |
| x264 | built from source at a pinned commit, because upstream publishes no release tags |
| Go modules | `go.mod` and `go.sum` |

Images are published for `amd64` and `arm64`, with cosign signatures and SBOM attestations, which [Checking a signature](https://github.com/cplieger/docs/blob/main/docs/images.md#checking-a-signature) and [Reading the software bill of materials](https://github.com/cplieger/docs/blob/main/docs/images.md#reading-the-software-bill-of-materials) show how to check. The license text of every bundled component is under `/usr/share/licenses/` in the image.
