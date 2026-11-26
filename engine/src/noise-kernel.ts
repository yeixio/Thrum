/**
 * Noise synthesis for one audio channel.
 *
 * IMPORTANT: this class must stay fully self-contained (no imports, no
 * references to anything outside the class). Its source text is injected
 * into the AudioWorklet, which runs in a separate global scope.
 */
export class NoiseKernel {
  private b0 = 0;
  private b1 = 0;
  private b2 = 0;
  private b3 = 0;
  private b4 = 0;
  private b5 = 0;
  private b6 = 0;
  private brown = 0;
  private lastPink = 0;
  private grainEnv = 0;
  private grainLp = 0;
  private readonly rng: () => number;

  constructor(rng?: () => number) {
    this.rng = rng ?? Math.random;
  }

  /**
   * Fills `out` with a weighted mix of noise colors. Each color is scaled
   * to a similar loudness (about 0.25 RMS) so crossfading between them
   * does not change the overall level much.
   */
  process(
    out: Float32Array,
    white: number,
    pink: number,
    brown: number,
    blue: number,
    grain: number,
    sampleRate: number,
  ): void {
    const rng = this.rng;
    // Up to roughly 60 crackles a second at full grain.
    const grainProb = (grain * grain * 60) / sampleRate;
    const grainDecay = Math.exp(-1 / (0.004 * sampleRate));
    for (let i = 0; i < out.length; i++) {
      const w = rng() * 2 - 1;

      // Pink: Paul Kellet's filter, about -3 dB per octave.
      this.b0 = 0.99886 * this.b0 + w * 0.0555179;
      this.b1 = 0.99332 * this.b1 + w * 0.0750759;
      this.b2 = 0.969 * this.b2 + w * 0.153852;
      this.b3 = 0.8665 * this.b3 + w * 0.3104856;
      this.b4 = 0.55 * this.b4 + w * 0.5329522;
      this.b5 = -0.7616 * this.b5 - w * 0.016898;
      const p = this.b0 + this.b1 + this.b2 + this.b3 + this.b4 + this.b5 + this.b6 + w * 0.5362;
      this.b6 = w * 0.115926;

      // Brown: leaky integrator, about -6 dB per octave.
      this.brown = (this.brown + 0.02 * w) / 1.02;

      // Blue: differentiated pink, about +3 dB per octave.
      const bl = p - this.lastPink;
      this.lastPink = p;

      let s = white * w * 0.43 + pink * p * 0.14 + brown * this.brown * 4.3 + blue * bl * 0.24;

      if (grainProb > 0 || this.grainEnv > 1e-4) {
        if (rng() < grainProb) this.grainEnv = 0.3 + 0.7 * rng();
        this.grainLp += 0.35 * (w - this.grainLp);
        s += this.grainEnv * this.grainLp * 0.9;
        this.grainEnv *= grainDecay;
      }
      out[i] = s;
    }
  }
}

export const NOISE_PROCESSOR_NAME = 'thrum-noise';

/** Source of the AudioWorklet module, built from the kernel above. */
export function noiseWorkletSource(): string {
  return `
const Kernel = (${NoiseKernel.toString()});
class ThrumNoiseProcessor extends AudioWorkletProcessor {
  static get parameterDescriptors() {
    const p = (name, v) => ({ name, defaultValue: v, minValue: 0, maxValue: 1, automationRate: 'k-rate' });
    return [p('white', 0), p('pink', 1), p('brown', 0), p('blue', 0), p('grain', 0)];
  }
  constructor() {
    super();
    this.kernels = [];
  }
  process(_inputs, outputs, params) {
    const out = outputs[0];
    for (let ch = 0; ch < out.length; ch++) {
      // One kernel per channel keeps left and right uncorrelated, which
      // is what makes the noise sound wide instead of stuck in the middle.
      const k = this.kernels[ch] || (this.kernels[ch] = new Kernel());
      k.process(out[ch], params.white[0], params.pink[0], params.brown[0], params.blue[0], params.grain[0], sampleRate);
    }
    return true;
  }
}
registerProcessor('${NOISE_PROCESSOR_NAME}', ThrumNoiseProcessor);
`;
}
