import { sanitizeState, EVENT_TYPES, type ClusterState, type EventType } from '@thrum/engine';

/** One update from the collector's /v1/stream endpoint. */
export interface LiveUpdate {
  cluster: string;
  time: string;
  state: ClusterState;
  detail: {
    nodesTotal: number;
    nodesNotReady: number;
    podsTotal: number;
    podsPending: number;
    podsCrashLooping: number;
    podsFailing: number;
    deploymentsTotal: number;
    deploymentsUnavailable: number;
    metricsAvailable: boolean;
    apiReachable: boolean;
    apiLatencyMs: number;
  };
}

export interface LiveHandlers {
  onUpdate(update: LiveUpdate): void;
  onEvent(type: EventType, name: string): void;
  onStatus(status: 'connecting' | 'connected' | 'reconnecting'): void;
}

/**
 * Subscribes to a Thrum collector. The browser reconnects by itself if the
 * collector restarts. Returns a function that disconnects.
 */
export function connectLive(baseUrl: string, token: string, handlers: LiveHandlers): () => void {
  const url = new URL('/v1/stream', baseUrl);
  if (token) url.searchParams.set('token', token);
  const source = new EventSource(url);
  handlers.onStatus('connecting');

  source.addEventListener('open', () => handlers.onStatus('connected'));
  source.addEventListener('error', () => handlers.onStatus('reconnecting'));
  source.addEventListener('state', (e) => {
    try {
      const update = JSON.parse((e as MessageEvent<string>).data) as LiveUpdate;
      update.state = sanitizeState(update.state);
      handlers.onUpdate(update);
    } catch {
      // Ignore a malformed update. The next one replaces it.
    }
  });
  source.addEventListener('event', (e) => {
    try {
      const ev = JSON.parse((e as MessageEvent<string>).data) as { type: EventType; name?: string };
      if (EVENT_TYPES.includes(ev.type)) handlers.onEvent(ev.type, ev.name ?? '');
    } catch {
      // Ignore.
    }
  });
  return () => source.close();
}
