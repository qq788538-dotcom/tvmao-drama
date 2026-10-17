<p align="center">
  <img src="web/public/logo.png" alt="Miaoju AI" width="96" />
</p>

<h1 align="center">Miaoju AI (喵剧AI)</h1>

<p align="center">
  A local-first infinite canvas for AI short-drama production: chain scripts, images, video and voice into one workflow.
</p>

<p align="center">
  <a href="README.md">中文</a> ·
  <a href="docs/user-guide.md">User guide (Chinese)</a> ·
  <a href="docs/development.md">Development (Chinese)</a>
</p>

---

## What it is

Miaoju AI is a desktop app for macOS and Windows. You place nodes on a canvas and connect them: uploaded images, videos, audio and text feed into AI image, video and audio generation nodes, and the results appear right on the canvas, ready to trim, crop, extend, upscale or pass to the next node.

- **Your data stays on your machine.** Projects, assets and generations live in a local data directory. No account, no cloud sync.
- **Bring your own models.** The app ships no model service. Point it at any gateway compatible with [New API](https://github.com/QuantumNous/new-api), add your API key, and every built-in model works. Individual models can use their own upstream.
- **AI assistants can drive the canvas.** A built-in agent understands the canvas, runs skills and creates nodes for you. The bundled `miaoju` CLI lets Claude Code, Codex and other coding assistants operate the canvas from a terminal.

## Features

- Infinite canvas with undo/redo, minimap, auto-arrange and a storyboard view
- AI image, video and audio nodes; 20 built-in models including Nano-Banana, gpt-image-2, Seedream 5.0, Seedance 2.0 / 2.5, MiniMax-H3 and more
- Image tools: grid split, annotate, rotate/mirror, relight, multi-angle, panorama
- Video tools: trim, crop, extend, reshoot, edit, upscale, subtitle removal, audio separation, frame extraction
- Script node that turns a screenplay or novel into shots and batch-generates video
- 3D director desk for blocking scenes and framing shots
- Generation history with ZIP export, asset library, and a built-in AI character library
- `miaoju` command-line tool plus a skill for AI coding assistants

## Download

Get the latest build from [Releases](../../releases):

| OS | Package | Requirement |
|---|---|---|
| macOS (Apple silicon) | `喵剧AI_<version>_aarch64.dmg` | macOS 14+ |
| macOS (Intel) | `喵剧AI_<version>_x64.dmg` | macOS 14+ |
| Windows | `miaoju-windows-x64-<version>.zip` | Windows 10 / 11 x64 |

The builds are not code-signed, so the OS will block the first launch. On macOS, allow it under System Settings → Privacy & Security; on Windows, click "More info → Run anyway".

## Quick start

1. Open the app. It creates an untitled project and opens the canvas.
2. Open Settings → Model Center and enter your gateway URL (e.g. `https://example.com`, no `/v1`) and API key.
3. Right-click the canvas → Add node → AI Image, write a prompt and generate.
4. Connect the image to an AI Video node to animate it.

The UI is currently in Chinese.

## Architecture

| Directory | Stack | Role |
|---|---|---|
| `src-tauri/` | Rust, Tauri 2 | Desktop shell: launches and supervises the sidecar, data-dir migration, native save dialogs, CLI install |
| `web/` | React 18, TypeScript, Vite, Ant Design | Canvas editor, nodes, agent panel, settings |
| `server/` | Go, SQLite, Eino | Local sidecar on `127.0.0.1`: REST + WebSocket API, generation queue, media storage, agent, skill runtime |
| `cli/` | Go | `miaoju` CLI talking to the running sidecar |

## Building from source

Requires Node.js 22, pnpm 10, Go 1.26, Rust stable and `tauri-cli` 2.12+. Development and packaging run on macOS; the Windows build is cross-compiled.

```sh
pnpm install
pnpm dev
pnpm test:server && pnpm test:web && pnpm test:tauri && pnpm test:scripts
```

See [docs/development.md](docs/development.md) for details.

## Contributing

Issues and pull requests are welcome; see [CONTRIBUTING.md](CONTRIBUTING.md). Please report security issues privately as described in [SECURITY.md](SECURITY.md).

## License

[MIT](LICENSE). Bundled third-party assets (such as the 3D mannequin model) keep their own licenses; see the license files next to them.

## Disclaimer

Miaoju AI is a creative tool and does not provide any model service. When generating content with third-party models, follow their terms of service and your local laws, and do not create content that infringes on others' likeness, copyright or other rights.
