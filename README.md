# Thrum

**Hear the health of your Kubernetes cluster.**

Thrum turns cluster telemetry into a continuous ambient soundscape. When everything is healthy it sounds peaceful. As the cluster comes under stress, degrades or fails, the sound changes with it, so you can keep an ear on your infrastructure without watching a dashboard.

> Status: early development. The sound engine exists and runs against a simulated cluster. Kubernetes integration is next.

## Principles

- **Never an alarm clock.** Trouble should be recognizable without being punishing. Dissonance has a ceiling, and everything fades rather than jumps.
- **Busy is not unhealthy.** Load changes the energy and texture of the sound. Only real problems add tension.
- **Health is independent of sound.** A health engine produces a normalized state. The sound engine only renders it.
- **Read-only.** Thrum will never need permission to change anything in a cluster.
- **The sound has to be good.** Everything is synthesized procedurally. There are no samples.

## Try the sound engine

Requires Node.js 20 or newer.

```sh
npm install
npm run dev
```

Open the printed URL, press **Start sound**, then try the scenarios and sliders.

## How the sound is built

Four layers are mixed into one output:

| Layer | What it is | Driven by |
|---|---|---|
| Base | Continuous noise in the profile you choose (white, pink, brown, blue, ocean, wind, rain, drone) | Your preference, shaped by CPU and memory |
| Workload | A brighter band of noise with a gentle pulse | CPU, network, disk |
| Anomaly | Tonal voices: consonant when healthy, dissonant when not | Conditions and overall health |
| Events | Short signatures, rate limited | Restarts, deployments, scaling, failures |

| Signal | What you hear |
|---|---|
| CPU | Intensity |
| Memory | Filter closes in. Near the limit, the sound saturates |
| Network | Pulse rate of the workload layer |
| Disk I/O | Grain and crackle |
| Pending pods | Slow beating against the home pitch |
| CrashLoopBackOff | A minor second |
| Failed workloads | A tritone |
| Node NotReady | The whole tonal center drops and darkens |
| API server latency | Pitch and filter drift |
| Critical health | Slow interruptions in the base layer |

State changes are smoothed with hysteresis and separate attack and decay times, so a brief spike barely registers and recovery settles slowly.

## Layout

```
engine/   Sound engine (TypeScript, Web Audio). No UI, no Kubernetes.
app/      Simulator UI for the engine. Becomes the desktop app.
```

Planned: `collector/` (Go, client-go) and `deploy/helm/`.

## Roadmap

1. **Sound engine**: procedural noise and smooth transitions between states.
2. **Kubernetes integration**: kubeconfig, telemetry, health model, driving the engine.
3. **Desktop experience**: Tauri app, system tray, cluster selection, health visuals.
4. **Advanced observability**: Prometheus, multiple clusters with their own sound identity, anomaly detection, custom profiles.

## Development

```sh
npm test            # engine unit tests
npm run typecheck
npm run build
```

## License

[Apache-2.0](LICENSE)
