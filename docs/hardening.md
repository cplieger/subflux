# Security

This page covers logins, reverse proxies, passkeys and what the subflux image contains, for readers who expose subflux beyond one machine or audit what they run.

## Logins and exposure

Login is on by default. On the first start, the first visitor to the page creates the admin account, so do that right after you start the container. After that, users sign in with a password, a passkey or OIDC single sign-on, and an admin can create API keys for the command line. Failed logins are rate-limited per client address.

`/metrics` and `/api/health` answer without a login, for Prometheus and for load balancers. Keep port 8374 on your own network, or put a reverse proxy with HTTPS in front of it.

`auth.disable_auth: true` turns login off and treats every request as an admin. subflux then logs a warning at the start and shows an alert on the web page until you remove the setting.

## What subflux checks

- Each subtitle site's address is checked before every fetch, so a site cannot make subflux request a service on your network.
- Saved site passwords and API keys are left out of every settings answer to the browser. An empty secret field on save keeps the stored value.
- Archive extraction refuses archives that expand far beyond their size.
- Every answer from an outside service is size-capped and checked before use.

## Running behind a reverse proxy

Behind a [reverse proxy](https://github.com/cplieger/docs/blob/main/docs/reverse-proxy.md), subflux sees the proxy's address and not the browser's. Set `trusted_proxies` to the proxy's address range. subflux then reads the real client address from a trusted `X-Forwarded-For` header. It uses that address for the audit log, the login rate limit, the session record and the access log, as in this example:

```yaml
trusted_proxies:
  - 192.0.2.0/24
  - 198.51.100.7/32
```

Entries are CIDR ranges. Write a single proxy as a `/32` for IPv4 or a `/128` for IPv6. `X-Forwarded-For` is read only when the direct peer is in one of these ranges, from right to left, so a client cannot fake its address. An invalid range is refused when the settings load. Leave `trusted_proxies` empty, the default, when nothing sits in front of subflux. The direct peer is then used and `X-Forwarded-For` is ignored.

## Blocking DNS rebinding

`allowed_hosts` lists the exact host names or addresses subflux answers for. A request whose `Host` header is not on the list is refused with 403 before it reaches any page:

```yaml
allowed_hosts:
  - subflux.example.com
  - 192.0.2.5
```

This closes a gap the cross-origin check leaves open. In a DNS rebinding attack, a malicious page makes its own host name resolve to subflux's address. The browser's request then carries the attacker's name in both `Origin` and `Host`, they agree, and the cross-origin check lets it through. Only an exact `Host` check stops it. Requests from localhost, such as the container healthcheck, always pass. Leave `allowed_hosts` empty, the default, to accept any `Host`.

## Passkeys and domain scope

A passkey belongs to a registrable domain, not to a host or a path. subflux takes that domain from the address you first save your settings from, so `subflux.example.com` gives `example.com`. The browser then offers the passkey on every host under `example.com`. A password manager cannot narrow that, because the scope is part of the passkey itself. If other applications share the domain, know that before you turn passkeys on.

A deployment under a path, such as `example.com/subflux`, gets the whole domain for the same reason. A passkey domain has no path part.

A deployment reached by IP address can never use passkeys, because an IP address is not a legal passkey domain. subflux does not set one, and the passkey controls stay disabled with the reason shown. The same holds for a one-word host name such as `nas`, and for a host that is itself a public suffix such as `duckdns.org`. A host under one, such as `mybox.duckdns.org`, works, scoped to exactly that host.

Passkeys need HTTPS, with one exception: `localhost` over plain HTTP, which the WebAuthn specification treats as trustworthy.

`auth.webauthn_rp_id` holds the domain, and you normally never set it. subflux fills it in on the first settings save, from the address you saved from. It is shown in the Authentication section of Settings, where you can narrow it to a single host. Changing it after passkeys exist makes them stop working, and the settings dialog asks before it lets you. Clearing the field does not turn passkeys off or lose the value. A save without a value keeps the stored one, and the dialog asks nothing.

One save is refused. You cannot change the field while the address you save from is outside the new value, because a passkey could never be tested from there. Save the change from a browser at a host inside the domain you want. A save that leaves the value alone is never refused, whatever address you are on.

The sign-in page shows its passkey button only when this server can complete a passkey login. That needs a passkey domain and at least one stored passkey for it. On a new install, or after you change the domain, sign in with your password and add a passkey from the Security dialog. The button then comes back.

## What the image contains

The image has no shell and no package manager. It runs as the user in your `compose.yaml`, or as `nonroot` (UID 65532) without one. Every pin below is tracked by automated pull requests. The two media libraries are built from source in the image, because no distribution ships them in the shape subflux needs.

| Component | Source |
| --- | --- |
| Base image | `gcr.io/distroless/static-debian13:nonroot`, pinned by digest |
| FFmpeg and ffprobe | built from source at a pinned release tag, statically linked, without network support |
| x264 | built from source at a pinned commit, because upstream publishes no release tags |
| Go modules | `go.mod` and `go.sum` |
| Web page packages | the `@cplieger/*` packages, pinned exactly in `internal/server/static-src/package.json` and the Dockerfile |

Images are published for `amd64` and `arm64`, with cosign signatures and SBOM attestations, which [Checking a signature](https://github.com/cplieger/docs/blob/main/docs/images.md#checking-a-signature) and [Reading the software bill of materials](https://github.com/cplieger/docs/blob/main/docs/images.md#reading-the-software-bill-of-materials) show how to check. The license text of every bundled component is under `/usr/share/licenses/` in the image.
