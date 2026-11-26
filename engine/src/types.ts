/**
 * The contract between a health engine and the sound engine.
 *
 * The sound engine never decides what "healthy" means. It is handed a
 * normalized state and renders it. Anything that can produce this shape
 * (the Kubernetes collector, a simulator, a test) can drive the sound.
 */

/** How busy the cluster is. Each value is 0..1. Busy is not unhealthy. */
export interface LoadState {
  cpu: number;
  memory: number;
  network: number;
  disk: number;
}

/** How unwell the cluster is. Each value is a 0..1 severity. */
export interface ConditionState {
  pendingPods: number;
  crashLoop: number;
  nodeNotReady: number;
  apiLatency: number;
  failedWorkloads: number;
}

export interface ClusterState {
  /** Overall health, 0 (critical) to 100 (healthy). */
  health: number;
  load: LoadState;
  conditions: ConditionState;
}

export type EventType =
  | 'podRestart'
  | 'deployment'
  | 'scaleUp'
  | 'scaleDown'
  | 'nodeFailure'
  | 'deploymentFailed';

export const EVENT_TYPES: readonly EventType[] = [
  'podRestart',
  'deployment',
  'scaleUp',
  'scaleDown',
  'nodeFailure',
  'deploymentFailed',
];

export const LOAD_KEYS = ['cpu', 'memory', 'network', 'disk'] as const;
export const CONDITION_KEYS = [
  'pendingPods',
  'crashLoop',
  'nodeNotReady',
  'apiLatency',
  'failedWorkloads',
] as const;

export function healthyState(): ClusterState {
  return {
    health: 100,
    load: { cpu: 0.15, memory: 0.3, network: 0.1, disk: 0.05 },
    conditions: {
      pendingPods: 0,
      crashLoop: 0,
      nodeNotReady: 0,
      apiLatency: 0,
      failedWorkloads: 0,
    },
  };
}

export function clamp(x: number, lo: number, hi: number): number {
  if (Number.isNaN(x)) return lo;
  return x < lo ? lo : x > hi ? hi : x;
}

/** Returns a copy with every field forced into its valid range. */
export function sanitizeState(s: ClusterState): ClusterState {
  const out = healthyState();
  out.health = clamp(s.health, 0, 100);
  for (const k of LOAD_KEYS) out.load[k] = clamp(s.load[k], 0, 1);
  for (const k of CONDITION_KEYS) out.conditions[k] = clamp(s.conditions[k], 0, 1);
  return out;
}
