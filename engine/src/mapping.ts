import type { Profile } from './profiles';
import { clamp, sanitizeState, type ClusterState } from './types';

/** Every number the synthesizer needs. All values are bounded. */
export interface SynthParams {
  /** Base noise layer. */
  baseGain: number;
  baseCutoff: number;
  baseQ: number;
  /** Saturation of the base layer, 0..1. */
  drive: number;
  grain: number;
  swellDepth: number;
  swellRate: number;
  wanderCents: number;

  /** Workload layer: a band of brighter noise that rises with activity. */
  workGain: number;
  tremRate: number;
  tremDepth: number;

  /** Anomaly layer. */
  bedGain: number;
  /** A voice a few Hz off the root. Heard as slow beating. */
  beatGain: number;
  /** Minor second above the root. */
  minorSecondGain: number;
  /** Tritone above the root. */
  tritoneGain: number;
  /** Multiplier on the root pitch. 1 is home. */
  rootRatio: number;
  /** Pitch drift of the tonal voices, in cents. */
  wobbleCents: number;
  /** Slow dips in the base layer when the cluster is critical, 0..1. */
  interruptDepth: number;
}

export interface MappingSettings {
  /** 0.5 (calm) to 2 (jumpy). 1 is the default. */
  sensitivity: number;
  /**
   * Ceiling on the combined level of dissonant voices, 0..1. This is what
   * keeps a bad day from becoming an alarm clock.
   */
  maxTension: number;
}

export const DEFAULT_MAPPING: MappingSettings = { sensitivity: 1, maxTension: 0.6 };

const lerp = (a: number, b: number, t: number) => a + (b - a) * t;

/**
 * Pure mapping from cluster state to synth parameters.
 *
 * Load only changes energy and texture. Conditions and health only change
 * tension and stability. That separation is the whole point: a cluster at
 * 90% CPU doing its job sounds lively, a cluster at 20% CPU with crashing
 * pods sounds wrong.
 */
export function mapStateToParams(
  raw: ClusterState,
  profile: Profile,
  settings: MappingSettings = DEFAULT_MAPPING,
): SynthParams {
  const s = sanitizeState(raw);
  const sens = clamp(settings.sensitivity, 0.25, 4);
  // Higher sensitivity lifts small severities so they are heard sooner.
  const sev = (x: number) => Math.pow(x, 1 / sens);

  const { cpu, memory, network, disk } = s.load;
  const pending = sev(s.conditions.pendingPods);
  const crash = sev(s.conditions.crashLoop);
  const node = sev(s.conditions.nodeNotReady);
  const latency = sev(s.conditions.apiLatency);
  const failed = sev(s.conditions.failedWorkloads);
  const unease = sev(1 - s.health / 100);
  // Memory only becomes a pressure signal near the top of its range.
  const memPressure = sev(clamp((memory - 0.8) / 0.2, 0, 1));
  const critical = clamp((30 - s.health) / 30, 0, 1);

  // Dissonant voices, then scaled together so they never exceed the ceiling.
  let beat = clamp(0.7 * pending + 0.3 * unease, 0, 1);
  let minorSecond = clamp(0.9 * crash + 0.25 * unease, 0, 1);
  let tritone = clamp(0.9 * failed + 0.4 * node, 0, 1);
  const total = beat + minorSecond + tritone;
  const ceiling = clamp(settings.maxTension, 0, 1) * 1.5;
  if (total > ceiling && total > 0) {
    const k = ceiling / total;
    beat *= k;
    minorSecond *= k;
    tritone *= k;
  }

  // Memory closes the filter by up to 1.5 octaves across its range.
  const cutoffOctaves = -1.5 * Math.pow(memory, 2) - 0.5 * node;

  return {
    baseGain: lerp(0.55, 0.85, cpu),
    baseCutoff: clamp(profile.cutoff * Math.pow(2, cutoffOctaves), 120, 16000),
    baseQ: profile.q + 2.5 * memPressure,
    drive: clamp(0.55 * memPressure + 0.2 * critical, 0, 0.75),
    grain: clamp(profile.grain + 0.55 * disk, 0, 1),
    swellDepth: clamp(profile.swellDepth, 0, 0.9),
    swellRate: profile.swellRate,
    wanderCents: profile.wanderCents + 600 * latency,

    workGain: 0.5 * Math.pow(cpu, 1.5),
    tremRate: lerp(0.08, 2.2, network),
    tremDepth: 0.1 + 0.25 * network,

    // The consonant bed comes forward as things go wrong, so the listener
    // has a "home" pitch to hear the dissonance against.
    bedGain: clamp(profile.bed + 0.45 * Math.max(unease, node, crash, failed, pending), 0, 1),
    beatGain: beat,
    minorSecondGain: minorSecond,
    tritoneGain: tritone,
    // A node going NotReady drops the whole tonal center by up to a fourth.
    rootRatio: lerp(1, 0.75, node),
    wobbleCents: clamp(45 * latency + 15 * unease, 0, 60),
    interruptDepth: 0.55 * critical,
  };
}
