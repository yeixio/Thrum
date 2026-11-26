# Contributing to Thrum

Thanks for your interest in Thrum! Whether you're fixing a typo, tuning a sound profile or building the Kubernetes collector, your help is welcome.

This guide covers how to get set up, how we work and what makes a pull request easy to merge.

## Ways to contribute

- **Pick up an issue.** Issues labelled [`good first issue`](https://github.com/yeixio/Thrum/issues?q=is%3Aissue+is%3Aopen+label%3A%22good+first+issue%22) are small, well scoped and a good way to learn the codebase. [`help wanted`](https://github.com/yeixio/Thrum/issues?q=is%3Aissue+is%3Aopen+label%3A%22help+wanted%22) issues are bigger.
- **Report a bug.** Use the [bug report form](https://github.com/yeixio/Thrum/issues/new?template=bug_report.yml).
- **Suggest a feature or a sound.** Use the [feature request form](https://github.com/yeixio/Thrum/issues/new?template=feature_request.yml). Ideas about how a signal *should* sound are just as valuable as code.
- **Improve the docs.** If something confused you, it will confuse the next person too.

Before starting on anything large, please open an issue or comment on an existing one so we can agree on the approach. It saves everyone time.

## Getting set up

You need [Node.js](https://nodejs.org/) 20 or newer and npm. The collector (coming in Phase 2) will need [Go](https://go.dev/) 1.23 or newer.

```sh
git clone https://github.com/<your-username>/Thrum.git
cd Thrum
npm install
npm run dev        # simulator UI with hot reload
```

Useful commands:

| Command | What it does |
|---|---|
| `npm run dev` | Starts the simulator UI |
| `npm test` | Runs the engine unit tests (Vitest) |
| `npm run typecheck` | Type checks `engine/` and `app/` |
| `npm run build` | Builds the app for production |

## Project layout

```
engine/   Sound engine (TypeScript, Web Audio). No UI and no Kubernetes knowledge.
app/      Simulator UI that drives the engine. Becomes the Tauri desktop app.
docs/     Documentation and images.
```

A few boundaries keep the project healthy:

- `engine/` only renders a normalized `ClusterState` (see `engine/src/types.ts`). It must never decide what "healthy" means.
- Thrum is **read-only** against clusters. Code that needs write access to Kubernetes will not be accepted.
- Sound is **synthesized**. Please don't add audio samples.

## Making a change

1. Fork the repository and create a branch from `main`, for example `fix/rain-crackle` or `feat/sound-profile-forest`.
2. Make your change. Add or update tests for anything in `engine/`.
3. Run `npm run typecheck && npm test && npm run build` locally.
4. Commit with a clear message. We loosely follow [Conventional Commits](https://www.conventionalcommits.org/): `feat:`, `fix:`, `docs:`, `test:`, `refactor:`, `chore:`.
5. Open a pull request and fill in the template. Link the issue it closes (`Closes #123`).

Small, focused pull requests are reviewed fastest. If a change grows, consider splitting it.

### Changes to the sound

Sound is subjective, so tell us what you were aiming for. In your pull request:

- Describe what the change should sound like and why, in plain words.
- Say which scenarios in the simulator you listened to.
- If you can, attach a short screen recording with audio.

Remember the principles: trouble must be recognizable but never punishing, and busy is not the same as unhealthy.

## Code style

- TypeScript is strict. Avoid `any`.
- Keep the engine free of DOM and UI code so it can run anywhere Web Audio does.
- Prefer small pure functions for mapping and smoothing logic; they are easy to test.
- Formatting follows [.editorconfig](.editorconfig): two spaces, LF line endings, final newline.

## Reporting security issues

Please don't open public issues for security problems. See [SECURITY.md](SECURITY.md).

## Code of Conduct

Everyone taking part in Thrum is expected to follow our [Code of Conduct](CODE_OF_CONDUCT.md).

## License

By contributing, you agree that your contributions will be licensed under the [MIT License](LICENSE).
