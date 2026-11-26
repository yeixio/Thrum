import {
  CONDITION_KEYS,
  LOAD_KEYS,
  PROFILES,
  PROFILE_NAMES,
  SoundscapeEngine,
  healthyState,
  type ClusterState,
  type EventType,
  type ProfileName,
} from '@thrum/engine';
import { deriveHealth } from './demo-health';
import { connectLive, type LiveUpdate } from './live';
import { SCENARIOS, type Scenario } from './scenarios';

const $ = <T extends HTMLElement>(id: string) => document.getElementById(id) as T;

let engine: SoundscapeEngine | null = null;
const state: ClusterState = healthyState();
let autoHealth = true;
let stormTimer: ReturnType<typeof setInterval> | null = null;

const settings = { profile: 'pink' as ProfileName, volume: 0.5, sensitivity: 1, response: 1, eventGap: 1.5 };

// Exposed for automated checks and for poking at the engine from the console.
declare global {
  interface Window {
    thrum: { engine: SoundscapeEngine | null; state: ClusterState };
  }
}
window.thrum = { engine: null, state };

// ---- sliders ---------------------------------------------------------

interface Slider {
  set(v: number): void;
}

function slider(
  parent: HTMLElement,
  label: string,
  opts: { min: number; max: number; step: number; value: number; format?: (v: number) => string },
  onInput: (v: number) => void,
): Slider {
  const row = document.createElement('label');
  row.className = 'row';
  const name = document.createElement('span');
  name.textContent = label;
  const input = document.createElement('input');
  input.type = 'range';
  input.min = String(opts.min);
  input.max = String(opts.max);
  input.step = String(opts.step);
  const out = document.createElement('output');
  const fmt = opts.format ?? ((v: number) => `${Math.round(v * 100)}%`);
  const show = (v: number) => {
    input.value = String(v);
    out.textContent = fmt(v);
  };
  show(opts.value);
  input.addEventListener('input', () => {
    const v = Number(input.value);
    out.textContent = fmt(v);
    onInput(v);
  });
  row.append(name, input, out);
  parent.append(row);
  return { set: show };
}

const LABELS: Record<string, string> = {
  cpu: 'CPU',
  memory: 'Memory',
  network: 'Network',
  disk: 'Disk I/O',
  pendingPods: 'Pending pods',
  crashLoop: 'CrashLoopBackOff',
  nodeNotReady: 'Node NotReady',
  apiLatency: 'API latency',
  failedWorkloads: 'Failed workloads',
};

const unit = { min: 0, max: 1, step: 0.01 };
const sliders: Record<string, Slider> = {};

for (const k of LOAD_KEYS) {
  sliders[k] = slider($('load'), LABELS[k]!, { ...unit, value: state.load[k] }, (v) => {
    state.load[k] = v;
    changed();
  });
}
for (const k of CONDITION_KEYS) {
  sliders[k] = slider($('conditions'), LABELS[k]!, { ...unit, value: state.conditions[k] }, (v) => {
    state.conditions[k] = v;
    changed();
  });
}
const healthSlider = slider(
  $('health-row'),
  'Health',
  { min: 0, max: 100, step: 1, value: 100, format: (v) => String(Math.round(v)) },
  (v) => {
    if (autoHealth) {
      autoHealth = false;
      $<HTMLInputElement>('auto-health').checked = false;
    }
    state.health = v;
    push();
  },
);

$<HTMLInputElement>('auto-health').addEventListener('change', (e) => {
  autoHealth = (e.target as HTMLInputElement).checked;
  changed();
});

function changed(): void {
  if (autoHealth) {
    state.health = deriveHealth(state);
    healthSlider.set(state.health);
  }
  push();
}

function push(): void {
  engine?.setState(state);
}

// ---- listening settings ----------------------------------------------

const profileSelect = $<HTMLSelectElement>('profile');
for (const name of PROFILE_NAMES) {
  const o = document.createElement('option');
  o.value = name;
  o.textContent = PROFILES[name].label;
  profileSelect.append(o);
}
profileSelect.value = settings.profile;
profileSelect.addEventListener('change', () => {
  settings.profile = profileSelect.value as ProfileName;
  engine?.setProfile(settings.profile);
});

slider($('listening'), 'Volume', { ...unit, value: settings.volume }, (v) => {
  settings.volume = v;
  engine?.setVolume(v);
});
slider(
  $('listening'),
  'Sensitivity',
  { min: 0.5, max: 2, step: 0.05, value: 1, format: (v) => `${v.toFixed(2)}x` },
  (v) => {
    settings.sensitivity = v;
    engine?.setSensitivity(v);
  },
);
slider(
  $('listening'),
  'Response time',
  { min: 0.1, max: 3, step: 0.05, value: 1, format: (v) => `${v.toFixed(2)}x` },
  (v) => {
    settings.response = v;
    engine?.setResponseScale(v);
  },
);
slider(
  $('listening'),
  'Event spacing',
  { min: 0.5, max: 10, step: 0.5, value: 1.5, format: (v) => `${v.toFixed(1)}s` },
  (v) => {
    settings.eventGap = v;
    engine?.setEventIntervals({ minInterval: v, minIntervalPerType: Math.max(v, 4) });
  },
);

// ---- scenarios and events --------------------------------------------

function applyScenario(sc: Scenario, button: HTMLButtonElement): void {
  const base = healthyState();
  Object.assign(state.load, base.load, sc.load);
  Object.assign(state.conditions, base.conditions, sc.conditions);
  for (const k of LOAD_KEYS) sliders[k]!.set(state.load[k]);
  for (const k of CONDITION_KEYS) sliders[k]!.set(state.conditions[k]);
  autoHealth = true;
  $<HTMLInputElement>('auto-health').checked = true;
  changed();
  $('scenario-note').textContent = sc.note;
  for (const b of $('scenarios').querySelectorAll('button')) b.classList.toggle('active', b === button);

  if (stormTimer) clearInterval(stormTimer);
  stormTimer = null;
  if (sc.repeatEvent) {
    const ev = sc.repeatEvent;
    // Fired far more often than the engine will play it, to show rate limiting.
    stormTimer = setInterval(() => fire(ev), 400);
  }
}

for (const sc of SCENARIOS) {
  const b = document.createElement('button');
  b.textContent = sc.name;
  b.addEventListener('click', () => applyScenario(sc, b));
  $('scenarios').append(b);
}

const EVENT_LABELS: Record<EventType, string> = {
  podRestart: 'Pod restart',
  deployment: 'Deployment',
  scaleUp: 'Scale up',
  scaleDown: 'Scale down',
  nodeFailure: 'Node failure',
  deploymentFailed: 'Deployment failed',
};

function fire(type: EventType): void {
  if (!engine?.isRunning) {
    $('event-note').textContent = 'Start the sound first.';
    return;
  }
  const played = engine.trigger(type);
  $('event-note').textContent = played
    ? `Played: ${EVENT_LABELS[type]}.`
    : `Held back: ${EVENT_LABELS[type]} (too soon after the last one). ${engine.suppressedEvents} held back so far.`;
}

for (const type of Object.keys(EVENT_LABELS) as EventType[]) {
  const b = document.createElement('button');
  b.textContent = EVENT_LABELS[type];
  b.addEventListener('click', () => fire(type));
  $('events').append(b);
}

// ---- live cluster -----------------------------------------------------

let disconnect: (() => void) | null = null;
const liveButton = $<HTMLButtonElement>('live-connect');
const liveStatus = $('live-status');
let lastUpdate: LiveUpdate | null = null;

function describe(u: LiveUpdate): string {
  const d = u.detail;
  const parts = [
    `${d.nodesTotal - d.nodesNotReady}/${d.nodesTotal} nodes ready`,
    `${d.podsTotal} pods`,
    `${d.deploymentsTotal} deployments`,
  ];
  if (d.podsCrashLooping) parts.push(`${d.podsCrashLooping} crash looping`);
  if (d.podsPending) parts.push(`${d.podsPending} pending`);
  if (d.podsFailing) parts.push(`${d.podsFailing} failing`);
  if (d.deploymentsUnavailable)
    parts.push(`${d.deploymentsUnavailable} ${d.deploymentsUnavailable === 1 ? 'deployment' : 'deployments'} unavailable`);
  if (!d.metricsAvailable) parts.push('no metrics-server, so CPU and memory are not heard');
  if (!d.apiReachable) parts.push('API server unreachable');
  return `Live: ${u.cluster || 'cluster'}. ${parts.join(', ')}.`;
}

function stopLive(): void {
  disconnect?.();
  disconnect = null;
  lastUpdate = null;
  document.body.classList.remove('live');
  liveButton.textContent = 'Connect to cluster';
  liveStatus.textContent = 'Simulator. Run thrum-collector and connect to hear a real cluster.';
  autoHealth = true;
  $<HTMLInputElement>('auto-health').checked = true;
  changed();
}

liveButton.addEventListener('click', () => {
  if (disconnect) return stopLive();
  if (stormTimer) clearInterval(stormTimer);
  stormTimer = null;
  let base: string;
  try {
    base = new URL($<HTMLInputElement>('live-url').value).toString();
  } catch {
    liveStatus.textContent = 'That address is not a valid URL.';
    return;
  }
  document.body.classList.add('live');
  liveButton.textContent = 'Disconnect';
  disconnect = connectLive(base, $<HTMLInputElement>('live-token').value, {
    onStatus(status) {
      if (status === 'connected' && lastUpdate) liveStatus.textContent = describe(lastUpdate);
      else if (status !== 'connected')
        liveStatus.textContent =
          status === 'connecting' ? 'Connecting…' : 'Cannot reach the collector. Retrying…';
    },
    onUpdate(u) {
      lastUpdate = u;
      Object.assign(state.load, u.state.load);
      Object.assign(state.conditions, u.state.conditions);
      state.health = u.state.health;
      for (const k of LOAD_KEYS) sliders[k]!.set(state.load[k]);
      for (const k of CONDITION_KEYS) sliders[k]!.set(state.conditions[k]);
      healthSlider.set(state.health);
      autoHealth = false;
      $<HTMLInputElement>('auto-health').checked = false;
      liveStatus.textContent = describe(u);
      push();
    },
    onEvent(type, name) {
      if (!engine?.isRunning) return;
      const played = engine.trigger(type);
      $('event-note').textContent = `${played ? 'Played' : 'Held back'}: ${EVENT_LABELS[type]}${name ? ` (${name})` : ''}.`;
    },
  });
});

// ---- power ------------------------------------------------------------

const power = $<HTMLButtonElement>('power');
power.addEventListener('click', async () => {
  power.disabled = true;
  try {
    if (!engine) {
      engine = await SoundscapeEngine.create({
        profile: settings.profile,
        volume: settings.volume,
        initialState: state,
        mapping: { sensitivity: settings.sensitivity },
        events: { minInterval: settings.eventGap, minIntervalPerType: Math.max(settings.eventGap, 4) },
      });
      engine.setResponseScale(settings.response);
      window.thrum.engine = engine;
    }
    if (engine.isRunning) engine.stop();
    else await engine.start();
    power.textContent = engine.isRunning ? 'Stop sound' : 'Start sound';
    power.setAttribute('aria-pressed', String(engine.isRunning));
  } catch (err) {
    console.error(err);
    $('scenario-note').textContent = `Could not start audio: ${String(err)}`;
  } finally {
    power.disabled = false;
  }
});

// ---- visuals ----------------------------------------------------------

const canvas = $<HTMLCanvasElement>('spectrum');
const g = canvas.getContext('2d')!;
const orb = $('orb');
const healthValue = $('health-value');
let bins: Uint8Array<ArrayBuffer> | null = null;

function draw(): void {
  requestAnimationFrame(draw);
  const heard = engine?.heardState ?? state;
  const hue = Math.round((heard.health / 100) * 165); // red at 0, teal at 100
  document.documentElement.style.setProperty('--health-hue', String(hue));
  healthValue.textContent = String(Math.round(heard.health));

  const w = canvas.width;
  const h = canvas.height;
  g.clearRect(0, 0, w, h);
  if (!engine?.isRunning) {
    orb.style.setProperty('--pulse', '1');
    return;
  }
  const an = engine.analyser;
  bins ??= new Uint8Array(an.frequencyBinCount);
  an.getByteFrequencyData(bins);

  // Log-frequency spectrum, 30 Hz to 16 kHz.
  const nyquist = engine.context.sampleRate / 2;
  const lo = Math.log(30);
  const hi = Math.log(16000);
  g.beginPath();
  g.moveTo(0, h);
  let energy = 0;
  for (let x = 0; x <= w; x += 3) {
    const f = Math.exp(lo + (x / w) * (hi - lo));
    const i = Math.min(bins.length - 1, Math.round((f / nyquist) * bins.length));
    const v = bins[i]! / 255;
    energy += v;
    g.lineTo(x, h - v * h * 0.95);
  }
  g.lineTo(w, h);
  g.closePath();
  g.fillStyle = `hsl(${hue} 60% 50% / 0.35)`;
  g.strokeStyle = `hsl(${hue} 65% 62%)`;
  g.lineWidth = 2;
  g.fill();
  g.stroke();
  orb.style.setProperty('--pulse', (0.94 + 0.12 * Math.min(1, energy / (w / 3))).toFixed(3));
}
draw();
