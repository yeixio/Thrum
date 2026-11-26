import { invoke } from '@tauri-apps/api/core';
import { listen } from '@tauri-apps/api/event';

/** True when running inside the Thrum desktop app instead of a browser. */
export const isDesktop = '__TAURI_INTERNALS__' in window;

export interface Contexts {
  current: string;
  contexts: string[];
}

export interface Connection {
  url: string;
  token: string;
}

/** Kubeconfig contexts on this machine. Does not contact any cluster. */
export const listContexts = () => invoke<Contexts>('list_contexts');

/** Starts the bundled collector for a context and resolves once it serves. */
export const startCollector = (context: string) => invoke<Connection>('start_collector', { context });

export const stopCollector = () => invoke<void>('stop_collector');

/** Shows health beside the tray icon. Pass null to clear it. */
export const setTray = (health: number | null, cluster: string | null) =>
  invoke<void>('set_tray', { health, cluster }).catch(() => {});

/** Calls back when "Start or stop sound" is chosen in the tray menu. */
export const onTrayToggleSound = (fn: () => void) => listen('tray-toggle-sound', fn);
