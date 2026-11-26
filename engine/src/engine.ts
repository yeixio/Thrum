import { playEvent } from './events';
import { DEFAULT_MAPPING, mapStateToParams, type MappingSettings, type SynthParams } from './mapping';
import { NOISE_PROCESSOR_NAME, noiseWorkletSource } from './noise-kernel';
import { PROFILES, type Profile, type ProfileName } from './profiles';
import {
  DEFAULT_EVENT_GATE,
  DEFAULT_SMOOTHER,
  EventGate,
  StateSmoother,
  type EventGateOptions,
  type SmootherOptions,
} from './smoothing';
import { clamp, healthyState, sanitizeState, type ClusterState, type EventType } from './types';

export interface EngineOptions {
  /** Supply a context to share one, or an OfflineAudioContext to render. */
  context?: BaseAudioContext;
  profile?: ProfileName;
  /** Master volume, 0..1. */
  volume?: number;
  /**
   * Root pitch of the tonal layer in Hz. Giving each cluster its own root
   * (and profile) is what gives each cluster its own sound identity.
   */
  rootHz?: number;
  mapping?: Partial<MappingSettings>;
  smoothing?: Partial<SmootherOptions>;
  events?: Partial<EventGateOptions>;
  initialState?: ClusterState;
}

interface Voice {
  osc: OscillatorNode;
  gain: GainNode;
  ratio: number;
  level: number;
}

interface Lfo {
  osc: OscillatorNode;
  depth: GainNode;
}

const CONTROL_INTERVAL_MS = 50;

/**
 * The soundscape: four layers mixed to one output.
 *
 *   base     continuous noise in the listener's chosen profile
 *   workload a brighter band of noise that follows activity
 *   anomaly  tonal voices that are consonant when healthy, dissonant when not
 *   events   short signatures for things that happen
 *
 * Feed it state with setState() and events with trigger(). It handles the
 * smoothing, so callers can push raw, jumpy values as often as they like.
 */
export class SoundscapeEngine {
  readonly context: BaseAudioContext;
  readonly analyser: AnalyserNode;

  private profileName: ProfileName;
  private volume: number;
  private rootHz: number;
  private mapping: MappingSettings;
  private readonly smootherOpts: SmootherOptions;
  private smoother: StateSmoother;
  private readonly gate: EventGate;
  private target: ClusterState;
  private smoothed: ClusterState;
  private params: SynthParams;
  private timer: ReturnType<typeof setInterval> | null = null;
  private lastTick = 0;
  private running = false;
  private stopTimer: ReturnType<typeof setTimeout> | null = null;

  // Nodes
  private baseNoise!: AudioWorkletNode;
  private workNoise!: AudioWorkletNode;
  private baseFilter!: BiquadFilterNode;
  private dryGain!: GainNode;
  private wetGain!: GainNode;
  private baseGain!: GainNode;
  private swellGain!: GainNode;
  private interruptGain!: GainNode;
  private workGain!: GainNode;
  private tremGain!: GainNode;
  private swell!: Lfo;
  private interrupt!: Lfo;
  private trem!: Lfo;
  private wander!: Lfo[];
  private wobble!: Lfo[];
  private voices!: { bed: Voice[]; beat: Voice; minorSecond: Voice; tritone: Voice };
  private eventBus!: GainNode;
  private master!: GainNode;
  private readonly sources: AudioScheduledSourceNode[] = [];

  private constructor(ctx: BaseAudioContext, opts: EngineOptions) {
    this.context = ctx;
    this.profileName = opts.profile ?? 'pink';
    this.volume = clamp(opts.volume ?? 0.5, 0, 1);
    this.rootHz = opts.rootHz ?? 110;
    this.mapping = { ...DEFAULT_MAPPING, ...opts.mapping };
    this.smootherOpts = { ...DEFAULT_SMOOTHER, ...opts.smoothing };
    this.gate = new EventGate({ ...DEFAULT_EVENT_GATE, ...opts.events });
    this.target = sanitizeState(opts.initialState ?? healthyState());
    this.smoothed = this.target;
    this.smoother = new StateSmoother(this.smootherOpts, this.target);
    this.params = mapStateToParams(this.smoothed, this.profile, this.mapping);
    this.analyser = ctx.createAnalyser();
    this.analyser.fftSize = 2048;
    this.analyser.smoothingTimeConstant = 0.85;
  }

  static async create(opts: EngineOptions = {}): Promise<SoundscapeEngine> {
    const ctx = opts.context ?? new AudioContext({ latencyHint: 'playback' });
    const url = URL.createObjectURL(new Blob([noiseWorkletSource()], { type: 'application/javascript' }));
    try {
      await ctx.audioWorklet.addModule(url);
    } finally {
      URL.revokeObjectURL(url);
    }
    const engine = new SoundscapeEngine(ctx, opts);
    engine.build();
    engine.apply(0);
    return engine;
  }

  private get profile(): Profile {
    return PROFILES[this.profileName];
  }

  // ---- graph ---------------------------------------------------------

  private lfo(rate: number, target: AudioParam): Lfo {
    const ctx = this.context;
    const osc = ctx.createOscillator();
    osc.frequency.value = rate;
    const depth = ctx.createGain();
    depth.gain.value = 0;
    osc.connect(depth).connect(target);
    osc.start();
    this.sources.push(osc);
    return { osc, depth };
  }

  private voice(ratio: number, level: number, dest: AudioNode): Voice {
    const ctx = this.context;
    const osc = ctx.createOscillator();
    osc.type = 'triangle';
    osc.frequency.value = this.rootHz * ratio;
    const gain = ctx.createGain();
    gain.gain.value = 0;
    osc.connect(gain).connect(dest);
    osc.start();
    this.sources.push(osc);
    return { osc, gain, ratio, level };
  }

  private noise(): AudioWorkletNode {
    return new AudioWorkletNode(this.context, NOISE_PROCESSOR_NAME, {
      numberOfInputs: 0,
      numberOfOutputs: 1,
      outputChannelCount: [2],
    });
  }

  private build(): void {
    const ctx = this.context;
    const bus = ctx.createGain();

    // Base layer: noise -> low-pass -> clean/saturated blend -> level -> swell -> dips
    this.baseNoise = this.noise();
    this.baseFilter = ctx.createBiquadFilter();
    this.baseFilter.type = 'lowpass';
    this.dryGain = ctx.createGain();
    this.wetGain = ctx.createGain();
    this.wetGain.gain.value = 0;
    const pre = ctx.createGain();
    pre.gain.value = 3;
    const shaper = ctx.createWaveShaper();
    const curve = new Float32Array(1024);
    for (let i = 0; i < curve.length; i++) curve[i] = Math.tanh(((i / (curve.length - 1)) * 2 - 1) * 2.5);
    shaper.curve = curve;
    shaper.oversample = '2x';
    this.baseGain = ctx.createGain();
    this.swellGain = ctx.createGain();
    this.interruptGain = ctx.createGain();
    this.baseNoise.connect(this.baseFilter);
    this.baseFilter.connect(this.dryGain).connect(this.baseGain);
    this.baseFilter.connect(pre).connect(shaper).connect(this.wetGain).connect(this.baseGain);
    this.baseGain.connect(this.swellGain).connect(this.interruptGain).connect(bus);

    this.swell = this.lfo(0.05, this.swellGain.gain);
    this.interrupt = this.lfo(0.31, this.interruptGain.gain);
    // Two slow oscillators at unrelated rates read as aimless drift.
    this.wander = [this.lfo(0.031, this.baseFilter.detune), this.lfo(0.073, this.baseFilter.detune)];

    // Workload layer: a band of bright noise with a gentle pulse.
    this.workNoise = this.noise();
    this.param(this.workNoise, 'pink').value = 0.4;
    this.param(this.workNoise, 'white').value = 0.6;
    const band = ctx.createBiquadFilter();
    band.type = 'bandpass';
    band.frequency.value = 2600;
    band.Q.value = 0.6;
    this.workGain = ctx.createGain();
    this.workGain.gain.value = 0;
    this.tremGain = ctx.createGain();
    this.workNoise.connect(band).connect(this.workGain).connect(this.tremGain).connect(bus);
    this.trem = this.lfo(0.1, this.tremGain.gain);

    // Anomaly layer: tonal voices through a soft low-pass.
    const toneFilter = ctx.createBiquadFilter();
    toneFilter.type = 'lowpass';
    toneFilter.frequency.value = 1400;
    toneFilter.connect(bus);
    this.voices = {
      bed: [this.voice(1, 0.2, toneFilter), this.voice(1.5, 0.12, toneFilter), this.voice(2, 0.07, toneFilter)],
      beat: this.voice(1.013, 0.2, toneFilter),
      minorSecond: this.voice((2 * 16) / 15, 0.16, toneFilter),
      tritone: this.voice(45 / 32, 0.18, toneFilter),
    };
    const allVoices = [...this.voices.bed, this.voices.beat, this.voices.minorSecond, this.voices.tritone];
    this.wobble = [0.11, 0.27].map((rate) => {
      const l = this.lfo(rate, allVoices[0]!.osc.detune);
      for (const v of allVoices.slice(1)) l.depth.connect(v.osc.detune);
      return l;
    });

    // Event layer.
    this.eventBus = ctx.createGain();
    this.eventBus.gain.value = 0.8;
    this.eventBus.connect(bus);

    // Output: volume, then a gentle limiter so nothing ever jumps out.
    this.master = ctx.createGain();
    this.master.gain.value = 0;
    const limiter = ctx.createDynamicsCompressor();
    limiter.threshold.value = -16;
    limiter.knee.value = 12;
    limiter.ratio.value = 8;
    limiter.attack.value = 0.01;
    limiter.release.value = 0.3;
    bus.connect(this.master).connect(limiter).connect(this.analyser).connect(ctx.destination);
  }

  private param(node: AudioWorkletNode, name: string): AudioParam {
    const p = node.parameters.get(name);
    if (!p) throw new Error(`noise worklet is missing parameter "${name}"`);
    return p;
  }

  /** Moves every audio parameter toward the current SynthParams. */
  private apply(tau: number): void {
    const now = this.context.currentTime;
    const p = this.params;
    const set = (param: AudioParam, v: number, t = tau) => {
      if (t <= 0) param.setValueAtTime(v, now);
      else param.setTargetAtTime(v, now, t);
    };
    // Profile changes crossfade slowly, whatever the control rate is.
    const slow = tau <= 0 ? 0 : Math.max(tau, 1.2);
    const mix = this.profile.mix;
    set(this.param(this.baseNoise, 'white'), mix.white, slow);
    set(this.param(this.baseNoise, 'pink'), mix.pink, slow);
    set(this.param(this.baseNoise, 'brown'), mix.brown, slow);
    set(this.param(this.baseNoise, 'blue'), mix.blue, slow);
    set(this.param(this.baseNoise, 'grain'), p.grain);
    set(this.param(this.workNoise, 'grain'), p.grain * 0.5);

    set(this.baseFilter.frequency, p.baseCutoff, slow > 0 ? Math.max(tau, 0.3) : 0);
    set(this.baseFilter.Q, p.baseQ);
    set(this.dryGain.gain, 1 - p.drive);
    set(this.wetGain.gain, p.drive * 0.7);
    set(this.baseGain.gain, p.baseGain);

    // Each modulated gain rests at (1 - depth/2) and swings by depth/2,
    // so it moves between (1 - depth) and 1 and never goes negative.
    const swing = (gain: AudioParam, lfo: Lfo, depth: number, t = tau) => {
      set(gain, 1 - depth / 2, t);
      set(lfo.depth.gain, depth / 2, t);
    };
    swing(this.swellGain.gain, this.swell, p.swellDepth, slow);
    set(this.swell.osc.frequency, p.swellRate, slow);
    swing(this.interruptGain.gain, this.interrupt, p.interruptDepth);
    swing(this.tremGain.gain, this.trem, p.tremDepth);
    set(this.trem.osc.frequency, p.tremRate);
    for (const l of this.wander) set(l.depth.gain, p.wanderCents / 2, slow);
    for (const l of this.wobble) set(l.depth.gain, p.wobbleCents / 2);

    set(this.workGain.gain, p.workGain);

    const glide = tau <= 0 ? 0 : Math.max(tau, 0.8);
    const tune = (v: Voice, amount: number) => {
      set(v.gain.gain, v.level * amount);
      set(v.osc.frequency, this.rootHz * p.rootRatio * v.ratio, glide);
    };
    for (const v of this.voices.bed) tune(v, p.bedGain);
    tune(this.voices.beat, p.beatGain);
    tune(this.voices.minorSecond, p.minorSecondGain);
    tune(this.voices.tritone, p.tritoneGain);
  }

  private tick = (): void => {
    const nowMs = performance.now();
    // Browsers throttle timers in background tabs. Using the real elapsed
    // time keeps the smoothing correct at any tick rate.
    const dt = Math.min((nowMs - this.lastTick) / 1000, 5);
    this.lastTick = nowMs;
    this.smoothed = this.smoother.update(this.target, dt);
    this.params = mapStateToParams(this.smoothed, this.profile, this.mapping);
    this.apply(clamp(dt / 2, 0.03, 0.5));
  };

  // ---- public API ----------------------------------------------------

  /** Starts (or resumes) sound with a fade in. Call from a user gesture. */
  async start(): Promise<void> {
    if (this.stopTimer) {
      clearTimeout(this.stopTimer);
      this.stopTimer = null;
    }
    const ctx = this.context;
    if ('resume' in ctx && ctx.state !== 'running') await (ctx as AudioContext).resume();
    this.running = true;
    this.fadeMaster(this.volume, 0.6);
    if (!this.timer) {
      this.lastTick = performance.now();
      this.timer = setInterval(this.tick, CONTROL_INTERVAL_MS);
    }
  }

  /** Fades out, then suspends the audio context to save power. */
  stop(): void {
    this.running = false;
    this.fadeMaster(0, 0.25);
    if (this.timer) {
      clearInterval(this.timer);
      this.timer = null;
    }
    const ctx = this.context;
    this.stopTimer = setTimeout(() => {
      this.stopTimer = null;
      if (!this.running && 'suspend' in ctx) void (ctx as AudioContext).suspend();
    }, 1200);
  }

  get isRunning(): boolean {
    return this.running;
  }

  private fadeMaster(to: number, tau: number): void {
    const now = this.context.currentTime;
    this.master.gain.cancelScheduledValues(now);
    this.master.gain.setTargetAtTime(to, now, tau);
  }

  /**
   * Sets the state the sound should move toward. With `immediate`, the
   * sound jumps there with no smoothing (for previews and offline renders).
   */
  setState(state: ClusterState, opts: { immediate?: boolean } = {}): void {
    this.target = sanitizeState(state);
    if (opts.immediate) {
      const scale = this.smoother.responseScale;
      this.smoother = new StateSmoother(this.smootherOpts, this.target);
      this.smoother.responseScale = scale;
      this.smoothed = this.target;
      this.params = mapStateToParams(this.smoothed, this.profile, this.mapping);
      this.apply(0);
    }
  }

  /**
   * Plays an event signature, unless one played too recently.
   * Returns whether it was played.
   */
  trigger(type: EventType): boolean {
    if (!this.gate.allow(type, this.context.currentTime)) return false;
    playEvent(this.context, this.eventBus, type, this.rootHz * this.params.rootRatio);
    return true;
  }

  setProfile(name: ProfileName): void {
    this.profileName = name;
  }

  setVolume(v: number): void {
    this.volume = clamp(v, 0, 1);
    if (this.running) this.fadeMaster(this.volume, 0.08);
  }

  /** For offline rendering: opens the master gain with no fade. */
  openOutput(): void {
    this.master.gain.setValueAtTime(this.volume, this.context.currentTime);
  }

  setSensitivity(s: number): void {
    this.mapping = { ...this.mapping, sensitivity: s };
  }

  /** 1 is the default. 0.25 reacts four times faster, 3 is very patient. */
  setResponseScale(scale: number): void {
    this.smoother.responseScale = clamp(scale, 0.05, 10);
  }

  setEventIntervals(opts: Partial<EventGateOptions>): void {
    this.gate.opts = { ...this.gate.opts, ...opts };
  }

  /** The state the listener is hearing right now, after smoothing. */
  get heardState(): ClusterState {
    return this.smoothed;
  }

  get currentParams(): SynthParams {
    return this.params;
  }

  get suppressedEvents(): number {
    return this.gate.suppressed;
  }

  async dispose(): Promise<void> {
    if (this.timer) clearInterval(this.timer);
    if (this.stopTimer) clearTimeout(this.stopTimer);
    this.timer = null;
    this.running = false;
    for (const s of this.sources) {
      try {
        s.stop();
      } catch {
        // already stopped
      }
    }
    this.master.disconnect();
    const ctx = this.context;
    if ('close' in ctx && ctx.state !== 'closed') await (ctx as AudioContext).close();
  }
}
