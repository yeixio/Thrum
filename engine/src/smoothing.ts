import {
  CONDITION_KEYS,
  LOAD_KEYS,
  healthyState,
  sanitizeState,
  type ClusterState,
  type EventType,
} from './types';

/**
 * Ignores input movement smaller than `band`, so a metric that jitters
 * around a value does not make the sound jitter with it.
 */
export class Hysteresis {
  private out: number;
  constructor(
    private readonly band: number,
    initial = 0,
  ) {
    this.out = initial;
  }
  update(x: number): number {
    if (x > this.out + this.band) this.out = x - this.band;
    else if (x < this.out - this.band) this.out = x + this.band;
    return this.out;
  }
}

/** One-pole follower with separate time constants for rising and falling. */
export class AttackDecay {
  constructor(public value: number) {}
  update(target: number, dt: number, riseSeconds: number, fallSeconds: number): number {
    const tau = target > this.value ? riseSeconds : fallSeconds;
    const k = tau <= 0 ? 1 : 1 - Math.exp(-dt / tau);
    this.value += (target - this.value) * k;
    return this.value;
  }
}

export interface SmootherOptions {
  /** Seconds for the sound to follow a state that is getting worse or busier. */
  attackSeconds: number;
  /** Seconds for the sound to settle once things recover. */
  decaySeconds: number;
  /** Dead band for 0..1 inputs. Health uses 100x this. */
  hysteresis: number;
}

export const DEFAULT_SMOOTHER: SmootherOptions = {
  attackSeconds: 5,
  decaySeconds: 12,
  hysteresis: 0.02,
};

interface Channel {
  hyst: Hysteresis;
  follow: AttackDecay;
}

/**
 * Turns raw, jumpy cluster state into a state that moves slowly enough to
 * listen to. A short spike barely registers. A sustained change arrives
 * over `attackSeconds` and leaves over `decaySeconds`.
 */
export class StateSmoother {
  private readonly load: Record<string, Channel> = {};
  private readonly cond: Record<string, Channel> = {};
  private readonly health: Channel;
  /** Multiplies both time constants. 1 is the default feel. */
  responseScale = 1;

  constructor(
    private readonly opts: SmootherOptions = DEFAULT_SMOOTHER,
    initial: ClusterState = healthyState(),
  ) {
    const init = sanitizeState(initial);
    const mk = (v: number, band: number): Channel => ({
      hyst: new Hysteresis(band, v),
      follow: new AttackDecay(v),
    });
    for (const k of LOAD_KEYS) this.load[k] = mk(init.load[k], opts.hysteresis);
    for (const k of CONDITION_KEYS) this.cond[k] = mk(init.conditions[k], opts.hysteresis);
    this.health = mk(init.health, opts.hysteresis * 100);
  }

  update(target: ClusterState, dt: number): ClusterState {
    const t = sanitizeState(target);
    const attack = this.opts.attackSeconds * this.responseScale;
    const decay = this.opts.decaySeconds * this.responseScale;
    const out = healthyState();
    for (const k of LOAD_KEYS) {
      const c = this.load[k]!;
      out.load[k] = c.follow.update(c.hyst.update(t.load[k]), dt, attack, decay);
    }
    for (const k of CONDITION_KEYS) {
      const c = this.cond[k]!;
      out.conditions[k] = c.follow.update(c.hyst.update(t.conditions[k]), dt, attack, decay);
    }
    // Health falling is the "attack" direction.
    out.health = this.health.follow.update(this.health.hyst.update(t.health), dt, decay, attack);
    return out;
  }
}

export interface EventGateOptions {
  /** Minimum seconds between any two event sounds. */
  minInterval: number;
  /** Minimum seconds between two sounds of the same type. */
  minIntervalPerType: number;
}

export const DEFAULT_EVENT_GATE: EventGateOptions = {
  minInterval: 1.5,
  minIntervalPerType: 4,
};

/**
 * Rate limits event sounds so a restart storm is heard as an occasional
 * signature, not a machine gun. The storm itself shows up in the
 * continuous layers through the condition scores.
 */
export class EventGate {
  private lastAny = -Infinity;
  private readonly lastByType = new Map<EventType, number>();
  suppressed = 0;

  constructor(public opts: EventGateOptions = DEFAULT_EVENT_GATE) {}

  allow(type: EventType, now: number): boolean {
    const lastOfType = this.lastByType.get(type) ?? -Infinity;
    if (now - this.lastAny < this.opts.minInterval || now - lastOfType < this.opts.minIntervalPerType) {
      this.suppressed++;
      return false;
    }
    this.lastAny = now;
    this.lastByType.set(type, now);
    return true;
  }
}
