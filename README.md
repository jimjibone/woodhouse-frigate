# woodhouse-frigate

A [Woodhouse](https://github.com/jimjibone/woodhouse-core) bridge that connects
to a [Frigate](https://frigate.video/) NVR instance and exposes its cameras as
Woodhouse devices (motion + camera services), streaming snapshots and motion
state into Woodhouse in real time.

## Usage

```sh
woodhouse-frigate --store <path> --addr <woodhouse-server-addr> --frigate <frigate-server-addr>
```

Key flags:

- `--store` (required) — path to local storage for pairing/config state
- `--addr` (required) — Woodhouse server address
- `--frigate` (required) — Frigate server address (e.g. `localhost:8088`)
- `--id` — bridge ID (default `frigate`)
- `--debug` / `-v` — enable debug logging

## Device shape (schema v2)

One device per Frigate camera, id `frigate<camera name>` (camera name
lowercased, reduced to `[a-z0-9]`; e.g. `garden-cam` is `frigategardencam`).

- Endpoint `0`: `info`, `online`.
- Endpoint `1`:
  - `camera` (feature `snapshot`): `image` fetched from Frigate's
    `/api/<camera>/latest.jpg` on request; `!detection{label, score?, zone?}`
    emitted when Frigate reports a new tracked object (`events` topic, type
    `new`, not a false positive). `zone` is the first current zone.
  - `motion`: `motion` follows `<camera>/motion` ON/OFF; `!detected` is
    emitted on each transition to ON.

## Taskfile tasks

- `task build` — build the bridge binary into `build-<os>-<arch>/`
- `task run` — build and run the bridge (extra args after `--` are passed through)
- `task init-deploy` — create/populate `.env` with the deploy variables (`DEPLOY_HOST`,
  `DEPLOY_USER`, `DEPLOY_DIR`, `DEPLOY_GOOS`, `DEPLOY_GOARCH`)
- `task deploy` — build for the configured deploy target, rsync it to the remote host, and
  restart its systemd service

`build-component` and `build-and-deploy-component` are internal helpers shared by the tasks
above; they aren't meant to be run directly.
