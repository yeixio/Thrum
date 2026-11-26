import { describe, expect, it } from 'vitest';
import {
  AttackDecay,
  EventGate,
  Hysteresis,
  NoiseKernel,
  PROFILES,
  PROFILE_NAMES,
  StateSmoother,
  healthyState,
  mapStateToParams,
  type ClusterState,
} from '../src/index';
import { noiseWorkletSource } from '../src/noise-kernel';

const pink = PROFILES.pink;

function state(patch: {
  health?: number;
  load?: Partial<ClusterState['load']>;
  conditions?: Partial<ClusterState['conditions']>;
}): ClusterState {
  const s = healthyState();
  return {
    health: patch.health ?? s.health,
    load: { ...s.load, ...patch.load },
    conditions: { ...s.conditions, ...patch.conditions },
  };
}

const tension = (p: ReturnType<typeof mapStateToParams>) => p.beatGain + p.minorSecondGain + p.tritoneGain;

describe('mapping: busy is not unhealthy', () => {
  it('a healthy cluster has no dissonance, wobble or interruption', () => {
    const p = mapStateToParams(healthyState(), pink);
    expect(tension(p)).toBe(0);
    expect(p.wobbleCents).toBe(0);
    expect(p.interruptDepth).toBe(0);
    expect(p.drive).toBe(0);
    expect(p.rootRatio).toBe(1);
  });

  it('90% CPU with no problems is more energetic but still consonant', () => {
    const idle = mapStateToParams(healthyState(), pink);
    const busy = mapStateToParams(state({ load: { cpu: 0.9, network: 0.7, disk: 0.5 } }), pink);
    expect(busy.baseGain).toBeGreaterThan(idle.baseGain);
    expect(busy.workGain).toBeGreaterThan(idle.workGain);
    expect(busy.tremRate).toBeGreaterThan(idle.tremRate);
    expect(busy.grain).toBeGreaterThan(idle.grain);
    expect(tension(busy)).toBe(0);
    expect(busy.interruptDepth).toBe(0);
  });

  it('20% CPU with crashing pods is quiet but dissonant', () => {
    const p = mapStateToParams(state({ health: 45, load: { cpu: 0.2 }, conditions: { crashLoop: 0.8 } }), pink);
    expect(p.minorSecondGain).toBeGreaterThan(0.3);
    expect(p.workGain).toBeLessThan(0.1);
  });

  it('memory closes the filter and only distorts near the top', () => {
    const low = mapStateToParams(state({ load: { memory: 0.3 } }), pink);
    const mid = mapStateToParams(state({ load: { memory: 0.75 } }), pink);
    const high = mapStateToParams(state({ load: { memory: 0.97 } }), pink);
    expect(mid.baseCutoff).toBeLessThan(low.baseCutoff);
    expect(high.baseCutoff).toBeLessThan(mid.baseCutoff);
    expect(mid.drive).toBe(0);
    expect(high.drive).toBeGreaterThan(0.2);
  });

  it('each condition has its own audible signature', () => {
    expect(mapStateToParams(state({ conditions: { pendingPods: 0.7 } }), pink).beatGain).toBeGreaterThan(0.3);
    expect(mapStateToParams(state({ conditions: { failedWorkloads: 0.7 } }), pink).tritoneGain).toBeGreaterThan(0.3);
    expect(mapStateToParams(state({ conditions: { nodeNotReady: 1 } }), pink).rootRatio).toBeCloseTo(0.75);
    expect(mapStateToParams(state({ conditions: { apiLatency: 0.8 } }), pink).wobbleCents).toBeGreaterThan(20);
    expect(mapStateToParams(state({ health: 5 }), pink).interruptDepth).toBeGreaterThan(0.3);
  });
});

describe('mapping: never an alarm clock', () => {
  const worst = state({
    health: 0,
    load: { cpu: 1, memory: 1, network: 1, disk: 1 },
    conditions: { pendingPods: 1, crashLoop: 1, nodeNotReady: 1, apiLatency: 1, failedWorkloads: 1 },
  });

  it('caps total dissonance at the comfort ceiling', () => {
    const p = mapStateToParams(worst, pink, { sensitivity: 2, maxTension: 0.6 });
    expect(tension(p)).toBeLessThanOrEqual(0.9 + 1e-9);
    const gentle = mapStateToParams(worst, pink, { sensitivity: 2, maxTension: 0.2 });
    expect(tension(gentle)).toBeLessThanOrEqual(0.3 + 1e-9);
  });

  it('keeps every parameter finite and in range for any input and profile', () => {
    const inputs = [worst, healthyState(), state({ health: NaN, load: { cpu: 7, memory: -3 } })];
    for (const name of PROFILE_NAMES) {
      for (const s of inputs) {
        const p = mapStateToParams(s, PROFILES[name], { sensitivity: 4, maxTension: 1 });
        for (const [k, v] of Object.entries(p)) expect(Number.isFinite(v), `${name}.${k}`).toBe(true);
        expect(p.baseGain).toBeLessThanOrEqual(0.85);
        expect(p.interruptDepth).toBeLessThanOrEqual(0.55);
        expect(p.drive).toBeLessThanOrEqual(0.75);
        expect(p.baseCutoff).toBeGreaterThanOrEqual(120);
      }
    }
  });

  it('higher sensitivity makes a small problem more audible', () => {
    const s = state({ conditions: { crashLoop: 0.2 } });
    const calm = mapStateToParams(s, pink, { sensitivity: 0.5, maxTension: 0.6 });
    const jumpy = mapStateToParams(s, pink, { sensitivity: 2, maxTension: 0.6 });
    expect(jumpy.minorSecondGain).toBeGreaterThan(calm.minorSecondGain);
  });
});

describe('smoothing', () => {
  it('hysteresis ignores jitter inside the band', () => {
    const h = new Hysteresis(0.05, 0.5);
    for (const x of [0.52, 0.48, 0.54, 0.46]) expect(h.update(x)).toBe(0.5);
    expect(h.update(0.7)).toBeCloseTo(0.65);
  });

  it('attack and decay use separate rates', () => {
    const up = new AttackDecay(0);
    const down = new AttackDecay(1);
    up.update(1, 1, 1, 10);
    down.update(0, 1, 1, 10);
    expect(up.value).toBeCloseTo(1 - Math.exp(-1));
    expect(down.value).toBeCloseTo(Math.exp(-0.1));
  });

  it('a two second CPU spike barely moves the sound', () => {
    const sm = new StateSmoother();
    const spike = state({ load: { cpu: 1 } });
    let out = healthyState();
    for (let t = 0; t < 2; t += 0.05) out = sm.update(spike, 0.05);
    expect(out.load.cpu).toBeLessThan(0.5);
    for (let t = 0; t < 60; t += 0.05) out = sm.update(healthyState(), 0.05);
    expect(out.load.cpu).toBeLessThan(0.2);
  });

  it('a sustained problem arrives, and recovery is slower than onset', () => {
    const sm = new StateSmoother();
    const bad = state({ health: 20, conditions: { crashLoop: 1 } });
    let out = healthyState();
    for (let t = 0; t < 5; t += 0.05) out = sm.update(bad, 0.05);
    const afterOnset = out.conditions.crashLoop;
    expect(afterOnset).toBeGreaterThan(0.55);
    expect(out.health).toBeLessThan(55);
    for (let t = 0; t < 5; t += 0.05) out = sm.update(healthyState(), 0.05);
    expect(out.conditions.crashLoop).toBeGreaterThan(1 - afterOnset);
  });

  it('gives the same result at 20 Hz and at a throttled 1 Hz', () => {
    const fast = new StateSmoother();
    const slow = new StateSmoother();
    const bad = state({ conditions: { pendingPods: 1 } });
    let a = healthyState();
    let b = healthyState();
    for (let i = 0; i < 200; i++) a = fast.update(bad, 0.05);
    for (let i = 0; i < 10; i++) b = slow.update(bad, 1);
    expect(a.conditions.pendingPods).toBeCloseTo(b.conditions.pendingPods, 5);
  });

  it('response scale stretches the response time', () => {
    const normal = new StateSmoother();
    const patient = new StateSmoother();
    patient.responseScale = 3;
    const bad = state({ conditions: { crashLoop: 1 } });
    let a = healthyState();
    let b = healthyState();
    for (let i = 0; i < 100; i++) {
      a = normal.update(bad, 0.05);
      b = patient.update(bad, 0.05);
    }
    expect(b.conditions.crashLoop).toBeLessThan(a.conditions.crashLoop);
  });
});

describe('event gate', () => {
  it('enforces a minimum interval between events', () => {
    const g = new EventGate({ minInterval: 1.5, minIntervalPerType: 4 });
    expect(g.allow('podRestart', 0)).toBe(true);
    expect(g.allow('deployment', 1)).toBe(false);
    expect(g.allow('deployment', 1.6)).toBe(true);
    expect(g.allow('podRestart', 3.5)).toBe(false);
    expect(g.allow('podRestart', 4.1)).toBe(true);
    expect(g.suppressed).toBe(2);
  });

  it('turns a restart storm into an occasional signature', () => {
    const g = new EventGate();
    let played = 0;
    for (let t = 0; t < 60; t += 0.1) if (g.allow('podRestart', t)) played++;
    expect(played).toBeLessThanOrEqual(15);
    expect(played).toBeGreaterThanOrEqual(10);
  });
});

describe('noise kernel', () => {
  // Deterministic generator so the measurements are repeatable.
  function lcg(seed: number) {
    let s = seed >>> 0;
    return () => ((s = (Math.imul(s, 1664525) + 1013904223) >>> 0) / 4294967296);
  }
  const SR = 48000;
  function render(white: number, pink: number, brown: number, blue: number, grain = 0) {
    const k = new NoiseKernel(lcg(42));
    const out = new Float32Array(SR * 4);
    k.process(out, white, pink, brown, blue, grain, SR);
    return out.subarray(SR); // drop the first second while filters settle
  }
  const rms = (x: Float32Array) => Math.sqrt(x.reduce((a, v) => a + v * v, 0) / x.length);
  /** Ratio of high-frequency energy (first difference) to total energy. */
  const brightness = (x: Float32Array) => {
    let d = 0;
    for (let i = 1; i < x.length; i++) d += (x[i]! - x[i - 1]!) ** 2;
    return d / x.reduce((a, v) => a + v * v, 0);
  };

  it('all four colors land at a similar loudness and never clip', () => {
    const mixes: [number, number, number, number][] = [[1, 0, 0, 0], [0, 1, 0, 0], [0, 0, 1, 0], [0, 0, 0, 1]];
    for (const mix of mixes) {
      const x = render(...mix);
      expect(rms(x), mix.join()).toBeGreaterThan(0.15);
      expect(rms(x), mix.join()).toBeLessThan(0.4);
      expect(x.reduce((a, v) => Math.max(a, Math.abs(v)), 0), mix.join()).toBeLessThan(1.5);
    }
  });

  it('orders the colors from dark to bright: brown, pink, white, blue', () => {
    const b = [render(0, 0, 1, 0), render(0, 1, 0, 0), render(1, 0, 0, 0), render(0, 0, 0, 1)].map(brightness);
    expect(b[0]).toBeLessThan(b[1]!);
    expect(b[1]).toBeLessThan(b[2]!);
    expect(b[2]).toBeLessThan(b[3]!);
  });

  it('grain adds energy and silence stays silent', () => {
    expect(rms(render(0, 0, 0, 0))).toBe(0);
    expect(rms(render(0, 0, 0, 0, 1))).toBeGreaterThan(0.005);
  });

  it('the worklet source is self-contained, valid JavaScript', () => {
    const registered: Record<string, unknown> = {};
    const run = new Function('AudioWorkletProcessor', 'registerProcessor', 'sampleRate', noiseWorkletSource());
    run(class {}, (name: string, cls: unknown) => (registered[name] = cls), SR);
    const Proc = registered['thrum-noise'] as new () => {
      process(i: unknown[], o: Float32Array[][], p: Record<string, number[]>): boolean;
    };
    const out = [[new Float32Array(128), new Float32Array(128)]];
    const ok = new Proc().process([], out, { white: [0], pink: [1], brown: [0], blue: [0], grain: [0] });
    expect(ok).toBe(true);
    expect(rms(out[0]![0]!)).toBeGreaterThan(0);
    expect(out[0]![0]).not.toEqual(out[0]![1]);
  });
});
