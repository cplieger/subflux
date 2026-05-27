import { describe, it, expect, vi } from "vitest";
import { get, set, batch, subscribe, effect, computed } from "./store.js";

describe("batch", () => {
  it("single set inside batch calls subscriber once with final value", () => {
    expect.assertions(2);
    const spy = vi.fn();
    subscribe("b_single", spy);
    batch(() => {
      set("b_single", "hello");
    });
    expect(spy).toHaveBeenCalledTimes(1);
    expect(spy).toHaveBeenCalledWith("hello");
  });

  it("multiple sets to same key calls subscriber once with last value", () => {
    expect.assertions(2);
    const spy = vi.fn();
    subscribe("b_multi", spy);
    batch(() => {
      set("b_multi", "a");
      set("b_multi", "b");
      set("b_multi", "c");
    });
    expect(spy).toHaveBeenCalledTimes(1);
    expect(spy).toHaveBeenCalledWith("c");
  });

  it("multiple sets to different keys calls each subscriber once", () => {
    expect.assertions(4);
    const spyA = vi.fn();
    const spyB = vi.fn();
    subscribe("b_diffA", spyA);
    subscribe("b_diffB", spyB);
    batch(() => {
      set("b_diffA", 1);
      set("b_diffB", 2);
    });
    expect(spyA).toHaveBeenCalledTimes(1);
    expect(spyA).toHaveBeenCalledWith(1);
    expect(spyB).toHaveBeenCalledTimes(1);
    expect(spyB).toHaveBeenCalledWith(2);
  });

  it("set with same value inside batch does not notify", () => {
    expect.assertions(1);
    set("b_same", "x");
    const spy = vi.fn();
    subscribe("b_same", spy);
    batch(() => {
      set("b_same", "x");
    });
    expect(spy).not.toHaveBeenCalled();
  });

  it("nested batch flushes only after outermost completes", () => {
    expect.assertions(2);
    const spy = vi.fn();
    subscribe("b_nested", spy);
    batch(() => {
      batch(() => {
        set("b_nested", "inner");
      });
      // subscriber should NOT have been called yet
      expect(spy).not.toHaveBeenCalled();
    });
    expect(spy).toHaveBeenCalledTimes(1);
  });

  it("exception inside batch still flushes pending notifications", () => {
    expect.assertions(2);
    const spy = vi.fn();
    subscribe("b_throw", spy);
    try {
      batch(() => {
        set("b_throw", "val");
        throw new Error("oops");
      });
    } catch {
      /* expected */
    }
    expect(spy).toHaveBeenCalledTimes(1);
    expect(spy).toHaveBeenCalledWith("val");
  });

  it("set outside batch calls subscriber immediately", () => {
    expect.assertions(2);
    const spy = vi.fn();
    subscribe("b_imm", spy);
    set("b_imm", "now");
    expect(spy).toHaveBeenCalledTimes(1);
    expect(spy).toHaveBeenCalledWith("now");
  });
});

describe("get/set/subscribe", () => {
  it("get returns undefined for unset key", () => {
    expect(get("gs_unset")).toBeUndefined();
  });

  it("get/set round-trip", () => {
    set("gs_rt", 42);
    expect(get("gs_rt")).toBe(42);
  });

  it("subscribe fires on change", () => {
    const spy = vi.fn();
    subscribe("gs_fire", spy);
    set("gs_fire", "v1");
    expect(spy).toHaveBeenCalledTimes(1);
    expect(spy).toHaveBeenCalledWith("v1");
  });

  it("subscribe does not fire when value unchanged", () => {
    set("gs_nofire", "same");
    const spy = vi.fn();
    subscribe("gs_nofire", spy);
    set("gs_nofire", "same");
    expect(spy).not.toHaveBeenCalled();
  });

  it("unsubscribe stops notifications", () => {
    const spy = vi.fn();
    const unsub = subscribe("gs_unsub", spy);
    set("gs_unsub", "a");
    unsub();
    set("gs_unsub", "b");
    expect(spy).toHaveBeenCalledTimes(1);
  });

  it("multiple subscribers each receive notifications", () => {
    const spy1 = vi.fn();
    const spy2 = vi.fn();
    subscribe("gs_multi", spy1);
    subscribe("gs_multi", spy2);
    set("gs_multi", "x");
    expect(spy1).toHaveBeenCalledWith("x");
    expect(spy2).toHaveBeenCalledWith("x");
  });
});

describe("effect", () => {
  const cases: {
    name: string;
    run: () => { spy: ReturnType<typeof vi.fn>; cleanup?: () => void };
    assert: (spy: ReturnType<typeof vi.fn>) => void;
  }[] = [
    {
      name: 'effect reads key "a" → re-runs when "a" changes',
      run: () => {
        set("eff_a1", "init");
        const spy = vi.fn();
        effect(() => {
          spy(get("eff_a1"));
        });
        spy.mockClear();
        set("eff_a1", "changed");
        return { spy };
      },
      assert: (spy) => {
        expect(spy).toHaveBeenCalledTimes(1);
        expect(spy).toHaveBeenCalledWith("changed");
      },
    },
    {
      name: 'effect reads "a" and "b" → re-runs when either changes',
      run: () => {
        set("eff_ab_a", 0);
        set("eff_ab_b", 0);
        const spy = vi.fn();
        effect(() => {
          spy(get("eff_ab_a"), get("eff_ab_b"));
        });
        spy.mockClear();
        set("eff_ab_a", 1);
        set("eff_ab_b", 1);
        return { spy };
      },
      assert: (spy) => {
        expect(spy).toHaveBeenCalledTimes(2);
      },
    },
    {
      name: 'dynamic deps: conditional read of "b" only when "a" is true',
      run: () => {
        set("eff_dyn_a", true);
        set("eff_dyn_b", "x");
        const spy = vi.fn();
        effect(() => {
          const a = get("eff_dyn_a");
          if (a) {
            get("eff_dyn_b");
          }
          spy();
        });
        spy.mockClear();
        set("eff_dyn_b", "y"); // should trigger (a is true)
        set("eff_dyn_a", false); // re-runs, now b not tracked
        spy.mockClear();
        set("eff_dyn_b", "z"); // should NOT trigger
        return { spy };
      },
      assert: (spy) => {
        expect(spy).not.toHaveBeenCalled();
      },
    },
    {
      name: "disposal stops re-runs",
      run: () => {
        set("eff_disp", 0);
        const spy = vi.fn();
        const unsub = effect(() => {
          spy(get("eff_disp"));
        });
        spy.mockClear();
        unsub();
        set("eff_disp", 1);
        return { spy };
      },
      assert: (spy) => {
        expect(spy).not.toHaveBeenCalled();
      },
    },
    {
      name: "effect that calls set() on different key → no infinite loop",
      run: () => {
        set("eff_src", 0);
        const spy = vi.fn();
        effect(() => {
          const v = get("eff_src") as number;
          set("eff_dst", v * 2);
          spy();
        });
        spy.mockClear();
        set("eff_src", 5);
        return { spy };
      },
      assert: (spy) => {
        expect(spy).toHaveBeenCalledTimes(1);
        expect(get("eff_dst")).toBe(10);
      },
    },
    {
      name: "effect that throws → error caught, other effects still work",
      run: () => {
        set("eff_throw", 0);
        set("eff_ok", 0);
        const errSpy = vi.spyOn(console, "error").mockImplementation(() => {
          /* noop */
        });
        effect(() => {
          get("eff_throw");
          throw new Error("boom");
        });
        const spy = vi.fn();
        effect(() => {
          spy(get("eff_ok"));
        });
        spy.mockClear();
        set("eff_ok", 1);
        errSpy.mockRestore();
        return { spy };
      },
      assert: (spy) => {
        expect(spy).toHaveBeenCalledTimes(1);
      },
    },
  ];

  for (const tc of cases) {
    it(tc.name, () => {
      const { spy } = tc.run();
      tc.assert(spy);
    });
  }
});

describe("computed", () => {
  const cases: {
    name: string;
    run: () => void;
    assert: () => void;
  }[] = [
    {
      name: "computed updates when dependency changes",
      run: () => {
        set("c_a", 1);
        set("c_b", 2);
        computed("c_sum", () => (get("c_a") as number) + (get("c_b") as number));
      },
      assert: () => {
        expect(get("c_sum")).toBe(3);
        set("c_a", 10);
        expect(get("c_sum")).toBe(12);
      },
    },
    {
      name: "computed result immediately available after creation",
      run: () => {
        set("c_imm", 5);
        computed("c_imm_out", () => (get("c_imm") as number) * 3);
      },
      assert: () => {
        expect(get("c_imm_out")).toBe(15);
      },
    },
    {
      name: "chained computed updates transitively",
      run: () => {
        set("c_base", 2);
        computed("c_double", () => (get("c_base") as number) * 2);
        computed("c_quad", () => (get("c_double") as number) * 2);
      },
      assert: () => {
        expect(get("c_quad")).toBe(8);
        set("c_base", 3);
        expect(get("c_quad")).toBe(12);
      },
    },
    {
      name: "disposal stops updates to output key",
      run: () => {
        set("c_disp_in", 1);
      },
      assert: () => {
        const unsub = computed("c_disp_out", () => (get("c_disp_in") as number) + 100);
        expect(get("c_disp_out")).toBe(101);
        unsub();
        set("c_disp_in", 2);
        expect(get("c_disp_out")).toBe(101);
      },
    },
    {
      name: "computed with no deps (constant) never re-runs after initial",
      run: () => {
        const spy = vi.fn(() => 42);
        computed("c_const", spy);
        expect(spy).toHaveBeenCalledTimes(1);
        set("c_unrelated", "x");
        expect(spy).toHaveBeenCalledTimes(1);
      },
      assert: () => {
        expect(get("c_const")).toBe(42);
      },
    },
  ];

  for (const tc of cases) {
    it(tc.name, () => {
      tc.run();
      tc.assert();
    });
  }
});
