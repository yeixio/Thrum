# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project aims to follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Procedural sound engine with base, workload, anomaly and event layers.
- Eight sound profiles: white, pink, brown, blue, ocean, wind, rain and atmospheric drone.
- Simulator UI with scenarios, load and condition sliders, and event triggers.
- Contributor documentation, issue and pull request templates.
- Read-only Kubernetes collector (`thrum-collector`) with a health model that separates load from health.
- Local HTTP API with server-sent events for state and cluster events.
- Live cluster mode in the app.
- Fake API server for trying live mode without a cluster.
- Desktop app (Tauri) with a bundled collector, a kubeconfig context picker and a system tray with a health readout.
- Release workflow that builds installers for macOS, Linux and Windows from a version tag.

### Changed

- License changed from Apache-2.0 to MIT.
