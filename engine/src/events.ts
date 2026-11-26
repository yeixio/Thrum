import type { EventType } from './types';

interface Note {
  /** Frequency as a ratio of the root. */
  ratio: number;
  /** Optional ratio to glide to over the note's length. */
  glideTo?: number;
  /** Start offset in seconds. */
  at: number;
  dur: number;
  peak: number;
  wave?: OscillatorType;
}

/**
 * Event signatures, written as ratios of the soundscape's root pitch so
 * they always sit in tune with the bed. Benign events use consonant
 * intervals. Failures use a minor second or a tritone.
 */
export const EVENT_SIGNATURES: Record<EventType, Note[]> = {
  podRestart: [{ ratio: 4, glideTo: 3, at: 0, dur: 0.28, peak: 0.1 }],
  deployment: [
    { ratio: 3, at: 0, dur: 0.5, peak: 0.09 },
    { ratio: 4, at: 0.14, dur: 0.7, peak: 0.09 },
  ],
  scaleUp: [
    { ratio: 2, at: 0, dur: 0.3, peak: 0.07 },
    { ratio: 2.5, at: 0.09, dur: 0.3, peak: 0.07 },
    { ratio: 3, at: 0.18, dur: 0.5, peak: 0.07 },
  ],
  scaleDown: [
    { ratio: 3, at: 0, dur: 0.3, peak: 0.07 },
    { ratio: 2.5, at: 0.09, dur: 0.3, peak: 0.07 },
    { ratio: 2, at: 0.18, dur: 0.5, peak: 0.07 },
  ],
  nodeFailure: [
    { ratio: 0.5, glideTo: 0.375, at: 0, dur: 1.6, peak: 0.3 },
    { ratio: 2, at: 0.05, dur: 1.2, peak: 0.05 },
    { ratio: (2 * 16) / 15, at: 0.05, dur: 1.2, peak: 0.05 },
  ],
  deploymentFailed: [
    { ratio: 3, at: 0, dur: 0.45, peak: 0.09 },
    { ratio: (3 * 32) / 45, at: 0.2, dur: 0.9, peak: 0.09, wave: 'triangle' },
  ],
};

/** Schedules one event signature. Every note has a soft attack and a tail. */
export function playEvent(
  ctx: BaseAudioContext,
  dest: AudioNode,
  type: EventType,
  rootHz: number,
  when = ctx.currentTime,
): void {
  for (const n of EVENT_SIGNATURES[type]) {
    const t0 = when + n.at;
    const osc = ctx.createOscillator();
    osc.type = n.wave ?? 'sine';
    osc.frequency.setValueAtTime(rootHz * n.ratio, t0);
    if (n.glideTo) osc.frequency.exponentialRampToValueAtTime(rootHz * n.glideTo, t0 + n.dur);
    const g = ctx.createGain();
    g.gain.setValueAtTime(0, t0);
    g.gain.linearRampToValueAtTime(n.peak, t0 + 0.02);
    g.gain.exponentialRampToValueAtTime(0.0001, t0 + n.dur);
    osc.connect(g).connect(dest);
    osc.start(t0);
    osc.stop(t0 + n.dur + 0.05);
  }
}
