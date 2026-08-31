// events-fakes.ts — the fake EventSource the events.ts suites drive. The
// real module reads EventSource.CLOSED, constructs instances with the
// endpoint path (cursor-bearing on recreates), and dispatches named frames
// with SSE ids; the fake records instances and mimics exactly that surface.
// No vitest imports: the suites own their mocking, this owns only the shape.

export class FakeEventSource {
  static readonly CONNECTING = 0;
  static readonly OPEN = 1;
  static readonly CLOSED = 2;
  static instances: FakeEventSource[] = [];

  url: string;
  readyState = FakeEventSource.CONNECTING;
  closed = false;
  private listeners = new Map<string, Set<(e: MessageEvent) => void>>();

  constructor(url: string) {
    this.url = url;
    FakeEventSource.instances.push(this);
  }

  addEventListener(type: string, fn: (e: MessageEvent) => void): void {
    let set = this.listeners.get(type);
    if (!set) {
      set = new Set();
      this.listeners.set(type, set);
    }
    set.add(fn);
  }

  close(): void {
    this.closed = true;
    this.readyState = FakeEventSource.CLOSED;
  }

  /** Dispatch a named SSE frame whose data is the JSON envelope
   *  {type, data: payload} the server publishes. A numeric id rides
   *  lastEventId, matching the browser's delivery of the id: field. */
  frame(type: string, payload: unknown, id?: number): void {
    this.dispatch(type, JSON.stringify({ type, data: payload }), id);
  }

  /** The per-connection epoch handshake (no id — the server omits the id:
   *  field, so lastEventId stays empty). */
  epoch(bootId: string, gap: boolean, head: number): void {
    this.frame("epoch", { boot_id: bootId, gap, head });
  }

  open(): void {
    this.readyState = FakeEventSource.OPEN;
    this.dispatch("open", "");
  }

  /** Server-side connection loss: browser EventSource flips to CLOSED
   *  before the error event when it will not retry on its own. */
  fail(): void {
    this.readyState = FakeEventSource.CLOSED;
    this.dispatch("error", "");
  }

  /** A transient error the browser retries on its own: it stays OPEN (or
   *  CONNECTING) and the module must not schedule a reconnect of its own. */
  errorWhileOpen(): void {
    this.readyState = FakeEventSource.OPEN;
    this.dispatch("error", "");
  }

  private dispatch(type: string, data: string, id?: number): void {
    const init: { data: string; lastEventId?: string } = { data };
    if (id !== undefined) {
      init.lastEventId = String(id);
    }
    const e = new MessageEvent(type, init);
    for (const fn of this.listeners.get(type) ?? new Set<(e: MessageEvent) => void>()) {
      fn(e);
    }
  }
}

/** The newest fake connection; throws when none exists. */
export function lastFakeES(): FakeEventSource {
  const es = FakeEventSource.instances.at(-1);
  if (!es) {
    throw new Error("no EventSource instance");
  }
  return es;
}
