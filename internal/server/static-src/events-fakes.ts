// events-fakes.ts — the fake SSE server the events.ts suites drive. The real
// module injects one fetch into the library's stream and digest client; the
// fake answers the three routes those make: the stream as a Response whose
// body is a ReadableStream the test writes frames into, the digest from a
// script, the alive acknowledgement with a 204. No vitest imports: the
// suites own their mocking, this owns only the shape.

import type { Held, Removed } from "@cplieger/sse";

export const EPOCH_A = "aaaaaaaaaaaaaaaa";
export const EPOCH_B = "bbbbbbbbbbbbbbbb";

interface HelloOptions {
  readonly head?: string | number;
  readonly resumed?: boolean;
  readonly verdict?: string;
}

/** One stream connection the module opened. */
class FakeConnection {
  readonly url: string;
  readonly headers: Headers;
  readonly readable: ReadableStream<Uint8Array>;
  ended = false;
  private controller!: ReadableStreamDefaultController<Uint8Array>;
  private readonly encoder = new TextEncoder();

  constructor(url: string, headers: Headers, signal: AbortSignal | undefined) {
    this.url = url;
    this.headers = headers;
    this.readable = new ReadableStream<Uint8Array>({
      start: (controller) => {
        this.controller = controller;
      },
    });
    // A real fetch tears the body down when its signal aborts.
    signal?.addEventListener("abort", () => {
      if (!this.ended) {
        this.ended = true;
        this.controller.error(new DOMException("aborted", "AbortError"));
      }
    });
  }

  /** The cursor this connection presented, as the header carries it. */
  get cursor(): string | null {
    return this.headers.get("Last-Event-ID");
  }

  write(text: string): void {
    if (this.ended) {
      throw new Error("write on an ended connection");
    }
    this.controller.enqueue(this.encoder.encode(text));
  }

  /** The handshake: the `retry:` line, then a well-formed sse:hello. */
  hello(epoch: string, opts: HelloOptions = {}): void {
    const head = String(opts.head ?? 0);
    const resumed = opts.resumed ?? false;
    const verdict = opts.verdict ?? (resumed ? "resumed" : "fresh");
    this.write("retry: 1500\n\n");
    const data = {
      wire: 1,
      epoch,
      floor: "0",
      head,
      resumed,
      verdict,
      keepalive_ms: 15000,
      keepalive_event: "sse:keepalive",
    };
    this.write(`event: sse:hello\ndata: ${JSON.stringify(data)}\n\n`);
  }

  /** An application frame whose data is the JSON envelope {type, data} the
   *  server publishes; `id` is the composite `<epoch>:<offset>` cursor. */
  frame(type: string, payload: unknown, id?: string): void {
    const lines = [`event: ${type}`];
    if (id !== undefined) {
      lines.push(`id: ${id}`);
    }
    lines.push(`data: ${JSON.stringify({ type, data: payload })}`);
    this.write(`${lines.join("\n")}\n\n`);
  }

  keepalive(): void {
    this.write("event: sse:keepalive\ndata: \n\n");
  }

  /** The server closes the stream (EOF). */
  end(): void {
    if (this.ended) {
      return;
    }
    this.ended = true;
    this.controller.close();
  }
}

/** One scripted digest answer; `must_refetch` wins, otherwise `checked`
 *  mirrors the request and the lists default to empty. */
interface DigestAnswer {
  readonly must_refetch?: boolean;
  readonly epoch?: string;
  readonly head?: string;
  readonly changed?: readonly Held[];
  readonly removed?: readonly Removed[];
}

interface DigestCall {
  readonly epoch: string | null;
  readonly subjects: readonly Held[];
  readonly headers: Headers;
}

export interface FakeSSE {
  readonly fetch: typeof fetch;
  readonly connections: FakeConnection[];
  /** The newest connection; throws when none exists. */
  last(): FakeConnection;
  /** When set, the next stream connect answers this status instead of a stream. */
  refuseStream: number | null;
  /** When set, every stream connect rejects like a network failure. */
  networkDown: boolean;
  /** When set, the digest POST answers this status instead of a body. */
  refuseDigest: number | null;
  readonly digestScript: DigestAnswer[];
  readonly digestCalls: DigestCall[];
  aliveCalls: number;
}

function pathOf(input: RequestInfo | URL): string {
  const url =
    typeof input === "string" ? input : input instanceof URL ? input.toString() : input.url;
  return new URL(url, location.origin).pathname;
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": "application/json" },
  });
}

/** A fresh fake server. */
export function fakeSSE(): FakeSSE {
  const connections: FakeConnection[] = [];
  const digestScript: DigestAnswer[] = [];
  const digestCalls: DigestCall[] = [];

  const state: FakeSSE = {
    connections,
    digestScript,
    digestCalls,
    refuseStream: null,
    networkDown: false,
    refuseDigest: null,
    aliveCalls: 0,
    last() {
      const c = connections.at(-1);
      if (!c) {
        throw new Error("no stream connection");
      }
      return c;
    },
    fetch: (input, init) => {
      const path = pathOf(input);
      if (path === "/api/events") {
        if (state.networkDown) {
          return Promise.reject(new TypeError("network down"));
        }
        if (state.refuseStream !== null) {
          return Promise.resolve(new Response(null, { status: state.refuseStream }));
        }
        const conn = new FakeConnection(
          pathOf(input),
          new Headers(init?.headers),
          init?.signal ?? undefined,
        );
        connections.push(conn);
        return Promise.resolve(
          new Response(conn.readable, {
            status: 200,
            headers: { "content-type": "text/event-stream" },
          }),
        );
      }
      if (path === "/api/events/sync") {
        if (state.refuseDigest !== null) {
          return Promise.resolve(new Response(null, { status: state.refuseDigest }));
        }
        if (typeof init?.body !== "string") {
          return Promise.reject(new Error("fake SSE server: digest body is not a string"));
        }
        const body = JSON.parse(init.body) as { epoch?: string; subjects: Held[] };
        digestCalls.push({
          epoch: body.epoch ?? null,
          subjects: body.subjects,
          headers: new Headers(init.headers),
        });
        const a = digestScript.shift() ?? {};
        const epoch = a.epoch ?? body.epoch ?? EPOCH_A;
        if (a.must_refetch === true) {
          return Promise.resolve(jsonResponse({ must_refetch: true, epoch, head: a.head ?? "0" }));
        }
        return Promise.resolve(
          jsonResponse({
            must_refetch: false,
            epoch,
            head: a.head ?? "0",
            checked: body.subjects.length,
            changed: a.changed ?? [],
            removed: a.removed ?? [],
          }),
        );
      }
      if (path === "/api/events/alive") {
        state.aliveCalls += 1;
        return Promise.resolve(new Response(null, { status: 204 }));
      }
      return Promise.reject(new Error(`fake SSE server: unexpected fetch ${path}`));
    },
  };
  return state;
}
