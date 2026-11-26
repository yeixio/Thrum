import type { ConditionState, EventType, LoadState } from '@thrum/engine';

export interface Scenario {
  name: string;
  note: string;
  load?: Partial<LoadState>;
  conditions?: Partial<ConditionState>;
  /** Fired repeatedly while the scenario is active. */
  repeatEvent?: EventType;
}

export const SCENARIOS: Scenario[] = [
  { name: 'Healthy and idle', note: 'The resting sound. Nothing to hear but the profile you chose.' },
  {
    name: 'Busy but healthy',
    note: '90% CPU, everything working. More energy and movement, no tension.',
    load: { cpu: 0.9, memory: 0.65, network: 0.7, disk: 0.5 },
  },
  {
    name: 'Memory pressure',
    note: 'The sound closes in and starts to saturate as memory runs out.',
    load: { cpu: 0.5, memory: 0.96 },
  },
  {
    name: 'Pods pending',
    note: 'A slow beating against the home pitch: something is waiting.',
    conditions: { pendingPods: 0.7 },
  },
  {
    name: 'Quiet but failing',
    note: '20% CPU with pods in CrashLoopBackOff. Low energy, clearly wrong. Restarts fire constantly but are rate limited.',
    load: { cpu: 0.2 },
    conditions: { crashLoop: 0.8, failedWorkloads: 0.3 },
    repeatEvent: 'podRestart',
  },
  {
    name: 'Slow API server',
    note: 'The pitch and filter start to drift. Unstable, not yet broken.',
    conditions: { apiLatency: 0.8 },
  },
  {
    name: 'Node down',
    note: 'The whole tonal center drops and darkens.',
    conditions: { nodeNotReady: 1 },
  },
  {
    name: 'Critical failure',
    note: 'Everything at once. Unsettled and interrupted, but capped so it never becomes an alarm.',
    load: { cpu: 0.4, memory: 0.9 },
    conditions: { pendingPods: 0.8, crashLoop: 1, nodeNotReady: 1, apiLatency: 0.9, failedWorkloads: 1 },
  },
];
