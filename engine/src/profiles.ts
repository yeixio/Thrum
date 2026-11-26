/**
 * A profile is the listener's choice of baseline sound. It is independent
 * of cluster health: health changes how the profile behaves, never which
 * profile is playing.
 */
export interface NoiseMix {
  white: number;
  pink: number;
  brown: number;
  blue: number;
}

export interface Profile {
  label: string;
  mix: NoiseMix;
  /** Resting low-pass cutoff in Hz. */
  cutoff: number;
  /** Resting filter resonance. */
  q: number;
  /** Slow volume swell: 0 is steady, 1 swells to silence. */
  swellDepth: number;
  swellRate: number;
  /** How far the filter drifts by itself, in cents. */
  wanderCents: number;
  /** Baseline crackle, 0..1. Disk activity adds to this. */
  grain: number;
  /** Level of the consonant tonal bed, 0..1. */
  bed: number;
}

export const PROFILES = {
  white: {
    label: 'White (crisp, airy)',
    mix: { white: 1, pink: 0, brown: 0, blue: 0 },
    cutoff: 9000, q: 0.5, swellDepth: 0, swellRate: 0.05, wanderCents: 0, grain: 0, bed: 0.15,
  },
  pink: {
    label: 'Pink (soft, balanced)',
    mix: { white: 0, pink: 1, brown: 0, blue: 0 },
    cutoff: 7000, q: 0.5, swellDepth: 0, swellRate: 0.05, wanderCents: 0, grain: 0, bed: 0.15,
  },
  brown: {
    label: 'Brown (deep, warm)',
    mix: { white: 0, pink: 0.15, brown: 1, blue: 0 },
    cutoff: 3500, q: 0.5, swellDepth: 0, swellRate: 0.05, wanderCents: 0, grain: 0, bed: 0.2,
  },
  blue: {
    label: 'Blue (bright, energetic)',
    mix: { white: 0.3, pink: 0, brown: 0, blue: 1 },
    cutoff: 12000, q: 0.5, swellDepth: 0, swellRate: 0.05, wanderCents: 0, grain: 0, bed: 0.15,
  },
  ocean: {
    label: 'Ocean',
    mix: { white: 0.1, pink: 0.45, brown: 0.8, blue: 0 },
    cutoff: 1400, q: 0.6, swellDepth: 0.6, swellRate: 0.085, wanderCents: 900, grain: 0, bed: 0.1,
  },
  wind: {
    label: 'Wind',
    mix: { white: 0, pink: 1, brown: 0.3, blue: 0 },
    cutoff: 800, q: 2.2, swellDepth: 0.35, swellRate: 0.06, wanderCents: 1400, grain: 0, bed: 0.1,
  },
  rain: {
    label: 'Rain',
    mix: { white: 0.45, pink: 0.5, brown: 0, blue: 0.1 },
    cutoff: 8000, q: 0.5, swellDepth: 0.12, swellRate: 0.04, wanderCents: 300, grain: 0.45, bed: 0.08,
  },
  drone: {
    label: 'Atmospheric drone',
    mix: { white: 0, pink: 0.2, brown: 0.7, blue: 0 },
    cutoff: 700, q: 0.7, swellDepth: 0.2, swellRate: 0.03, wanderCents: 500, grain: 0, bed: 0.75,
  },
} as const satisfies Record<string, Profile>;

export type ProfileName = keyof typeof PROFILES;
export const PROFILE_NAMES = Object.keys(PROFILES) as ProfileName[];
