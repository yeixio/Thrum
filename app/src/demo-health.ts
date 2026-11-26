import { clamp, type ClusterState } from '@thrum/engine';

/**
 * A stand-in health score for the Phase 1 simulator only.
 *
 * The real health model belongs to the collector (Phase 2). The sound
 * engine never computes health, it only renders what it is given.
 * Note that CPU, network and disk do not appear here: busy is not unhealthy.
 */
export function deriveHealth(s: ClusterState): number {
  const c = s.conditions;
  const memPressure = clamp((s.load.memory - 0.85) / 0.15, 0, 1);
  const penalty =
    0.35 * c.pendingPods +
    0.5 * c.crashLoop +
    0.6 * c.nodeNotReady +
    0.3 * c.apiLatency +
    0.5 * c.failedWorkloads +
    0.25 * memPressure;
  return Math.round(100 * (1 - clamp(penalty, 0, 1)));
}
