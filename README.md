<p align="center">
  <a href="https://github.com/owncast/owncast" alt="Owncast">
    <img src="https://owncast.online/images/logo.png" alt="Owncast Logo" width="200">
  </a>
</p>

<p align="center">
	<strong>Take control over your content and stream it yourself.</strong>
</p>

<br/>

<p align="center">
	<a href="https://github.com/owncast/owncast/blob/develop/LICENSE">
    <img src="https://img.shields.io/badge/License-MIT-green.svg" alt="License" />
  </a>
</p>

<br/>

<p align="center">
	<a href="https://owncast.online"><strong>Explore the docs »</strong></a>
	<br />
	<a href="https://watch.owncast.online/">View Demo</a>
	·
	<a href="https://owncast.online/faq/">FAQ</a>
	·
	<a href="https://github.com/owncast/owncast/issues">Report Bug</a>
	·
	<a href="docs/SECURITY.md">Report Security Issue</a>
</p>

<!-- TABLE OF CONTENTS -->

## Table of Contents

- 📒 [About the Project](#about-the-project)
- 🚀 [Getting Started](#getting-started)
- 👨‍💻 [Use with your broadcasting software](#use-with-your-existing-broadcasting-software)
- [SRT and WebRTC in this fork](#srt-and-webrtc-in-this-fork)
- 🛠 [Building from source](#building-from-source)
  - 🚨 [Important note about source code and the develop branch](#important-note-about-source-code-and-the-develop-branch)
  - 🗄️ [Backend](#backend)
  - ⚛️ [Frontend](#frontend)
  - [Release binary](#release-binary)
- 👏 [Contributing](#contributing)
  - 💵 [Donors](#donors)
- [Security](#security)
- 📝 [License](#license)
- [Contact](#contact)

<!-- ABOUT THE PROJECT -->

## About The Project

<p align="center">
  <a href="https://owncast.online/images/owncast-splash.png">
    <img src="https://owncast.online/images/owncast-splash.png" width="70%">
  </a>
</p>

Owncast is an open source, self-hosted, decentralized, single user live video streaming and chat server for running your own live streams similar in style to the large mainstream options. It offers complete ownership over your content, interface, moderation and audience. <a href="https://watch.owncast.online">Visit the demo</a> for an example.

<div>
    <img alt="GitHub all releases" src="https://img.shields.io/github/downloads/owncast/owncast/total?style=for-the-badge">
	  <a href="https://hub.docker.com/r/owncast/owncast">
      <img alt="Docker Pulls" src="https://img.shields.io/docker/pulls/owncast/owncast?style=for-the-badge">
	  </a>
    <a href="https://github.com/owncast/owncast/issues?q=is%3Aissue+is%3Aopen+label%3A%22good+first+issue%22">
      <img alt="GitHub issues by-label" src="https://img.shields.io/github/issues-raw/owncast/owncast/good%20first%20issue?style=for-the-badge">
    </a>
    <a href="https://opencollective.com/owncast">
      <img alt="Open Collective backers and sponsors" src="https://img.shields.io/opencollective/all/owncast?style=for-the-badge">
    </a>
</div>

---

<!-- GETTING STARTED -->

## Getting Started

The goal is to have a single service that you can run and it works out of the box. **Visit the [Quickstart](https://owncast.online/docs/quickstart/) to get up and running.**

## Use with your existing broadcasting software

In general, Owncast is compatible with any software that uses `RTMP` to broadcast to a remote server. `RTMP` is what all the major live streaming services use, so if you’re currently using one of those it’s likely that you can point your existing software at your Owncast instance instead.

OBS, Streamlabs, Restream and many others have been used with Owncast. [Read more about compatibility with existing software](https://owncast.online/docs/broadcasting/).

## SRT and WebRTC in this fork

The `feat/srt-webrtc-develop` branch adds SRT ingest alongside RTMP and low-latency WebRTC playback alongside HLS. SRT and WebRTC are disabled by default. Enable and configure them under **Admin → Live Streaming** (`/admin/config/live-streaming/`).

- **SRT ingest** accepts MPEG-TS from SRT broadcasters. The default listener port is UDP `8890`. With the raw-key stream ID format enabled, a publisher can use `srt://yourserver:8890?mode=caller&streamid=YOUR_STREAM_KEY`. If you configure an SRT passphrase, set the same passphrase in the publisher.
- **WebRTC playback** uses WHEP signaling over HTTP(S). The browser also needs network access to the configured WebRTC media ports or a working relay. Routing the WHEP HTTP endpoint through a reverse proxy or Cloudflare Tunnel does not by itself make the media ports reachable. The built-in player can fall back to HLS when WebRTC cannot connect.
- **Live Stream Status** shows ingest and output state, rendition details, and per-session viewer statistics. For WebRTC, the viewer table's **Jitter buffer** value is the average receiver buffer delay, not end-to-end latency.
- **Output and appearance settings** include per-rendition protocol selection, shared or isolated transcoding, and support for a custom player module or viewer frontend. See the [custom player example](docs/examples/custom-player/README.md).

### Deployment notes

**Viewer addresses behind a proxy.** This branch uses the direct connection address unless proxy headers are explicitly enabled. Set `OWNCAST_TRUST_PROXY_HEADERS` on the Owncast service to the header names your proxy reliably sets. Set `OWNCAST_TRUSTED_PROXY_RANGES` to the addresses of the proxies that connect directly to Owncast, for example:

```dotenv
OWNCAST_TRUST_PROXY_HEADERS=x-real-ip
OWNCAST_TRUSTED_PROXY_RANGES=127.0.0.1/32
```

Use the example only where a trusted local proxy overwrites `X-Real-IP`. If the proxy connects from another address, use a CIDR for that address (`/32` for IPv4 or `/128` for IPv6). For Cloudflare Tunnel, either pass `CF-Connecting-IP` through a verified Cloudflare-only path and enable `cf-connecting-ip`, or have your trusted proxy normalize the address into `X-Real-IP`. If the same proxy handles direct public traffic, strip Cloudflare-specific headers on that entry point. Restart Owncast after changing the environment and verify the viewer address on a new session through each entry point.

**FFmpeg and container storage.** FFmpeg must be available at runtime. Verify ingest, playback and thumbnail generation with the FFmpeg build used on the deployment host. When using the repository's `Dockerfile`, ensure any bind-mounted data directory is writable by the container's Owncast user (UID `101`).

## Building from Source

Owncast consists of two projects.

1. The Owncast backend is written in Go.
1. The frontend is written in React.

[Read more about running from source](https://owncast.online/development/).

### Important note about source code and the develop branch

The `develop` branch is always the most up-to-date state of development and this may not be what you always want. If you want to run the latest released stable version, check out the tag related to that release. For example, if you'd only like the source prior to the v0.1.0 development cycle you can check out the `v0.0.13` tag.

> Note: Currently Owncast does not natively support Windows servers. However, Windows Users can use Windows Subsystem for Linux (WSL2) to install Owncast. For details visit [this document](https://github.com/owncast/owncast/blob/develop/contrib/owncast_for_windows.md).

### Backend

The Owncast backend is a service written in Go.

1. Ensure you have prerequisites installed.
   - C compiler, such as [GCC compiler](https://gcc.gnu.org/install/download.html) or a [Musl-compatible compiler](https://musl.libc.org/)
   - [ffmpeg](https://ffmpeg.org/download.html)
1. Install [Go](https://golang.org/dl/) 1.26.2 or newer, as required by [`go.mod`](go.mod).
1. Clone this fork, enter its root directory, and start the backend:

   ```bash
   git clone --branch feat/srt-webrtc-develop https://github.com/Jiabailisan/owncast.git
   cd owncast
   go run .
   ```

1. Visit `http://yourserver:8080` to access the web interface or `http://yourserver:8080/admin` to access the admin.
1. Point your [broadcasting software](https://owncast.online/docs/broadcasting/) at your new server and start streaming.

### Frontend

The frontend is the web interface that includes the player, chat, embed components, and other UI.

With the backend running, use Node.js 24.12.0 (the CI version) and install the frontend dependencies from the lockfile using npm 11.6.2:

```bash
cd web
npx --yes npm@11.6.2 ci
npm run dev
```

Open `http://localhost:3000`. The frontend development server uses `http://localhost:8080` for backend requests by default. Set `OWNCAST_DEV_BACKEND` before starting the frontend if the backend uses another address.

### Release binary

From the repository root, run `./build/release.sh` or `make release` with the Go, C compiler and Node.js prerequisites installed. The script builds the checked-out commit in a temporary Git worktree, installs frontend dependencies from its lockfile, and embeds a fresh frontend in `bin/owncast`. Commit any intended source changes before running it.

Direct Go builds and the repository's Docker and Earthly builds embed the committed `static/web` files. After changing frontend source, rebuild the bundle with `./build/web/bundleWeb.sh --offline` after installing the locked dependencies, and include the updated `static/web` in the commit. CI checks the committed bundle against a fresh build.

## Contributing

Owncast is a growing open source project that is giving freedom, flexibility and fun to live streamers.
And while we have a small team of kind, talented and thoughtful volunteers, we have gaps in our skillset that we’d love to fill so we can get even better at building tools that make a difference for people.

We abide by our [Code of Conduct](https://owncast.online/contribute/) and feel strongly about open, appreciative, and empathetic people joining us.
We’ve been very lucky to have this so far, so maybe you can help us with your skills and passion, too!

If you're new to the project, maybe you'd be interested in looking at [![Good First Issue](https://img.shields.io/github/issues/owncast/owncast/good%20first%20issue.svg)](https://github.com/owncast/owncast/issues?q=is%3Aissue+is%3Aopen+label%3A%22good+first+issue%22).

There is a larger, more detailed, and more up-to-date [guide for helping contribute to Owncast on our website](https://owncast.online/help/).

### Donors

The Owncast project is possible thanks to the people who make a donation to support us and our work.
Thank you to all our donors who help keep Owncast running by donating on OpenCollective. You can support this project by [becoming a backer/sponsor](https://opencollective.com/owncast#suppor).

<div>
	<a href="https://opencollective.com/owncast#support">
		<img alt="GitHub issues by-label" src="https://opencollective.com/owncast/tiers/backers.svg?avatarHeight=36&width=600" alt="Backer button">
	</a>
</div>

<!-- SECURITY -->

## Security

Report suspected security vulnerabilities privately to [security@owncast.online](mailto:security@owncast.online). This is the security-reporting address. The personal contact information in the Support section is for general contact, not vulnerability reports.

See the repository [security policy](docs/SECURITY.md) for scope, disclosure, and credit details.

<!-- LICENSE -->

## License

Distributed under the MIT License. See `LICENSE` for more information.

## Support

<ul style="font-size:21px; color:black; ">
<li>Project chat provided by
<a href="https://rocket.chat" target="_blank">
<img src="https://owncast.online/images/sponsors/rocketchat.png" width="147" height="26" style="vertical-align: middle;margin-left:5px">
</a>
</li>
<li>CDN services by
<a href="https://fastly.com" target="_blank">
<img src="https://owncast.online/images/sponsors/fastly.png" height="26" style="vertical-align: middle;margin-left:5px">
</a>
</li>
<li>UI testing with Chromatic
<a href="https://chromatic.com" target="_blank">
<img src="https://owncast.online/images/sponsors/chromatic.png" height="26" style="vertical-align: middle;margin-left:5px">
</a>
</li>
<li>Infrastructure and hosting by
<a href="https://digitalocean.com?utm_medium=opensource&utm_source=owncast" target="_blank">
<img src="https://owncast.online/images/sponsors/digitalocean.svg" height="26" style="vertical-align: middle;margin-left:5px">
</a>
</li>
</ul>
<!-- CONTACT -->

## Contact

Project chat: [Join us on Rocket.Chat](https://owncast.rocket.chat/home) if you want to contribute, follow along, or if you have questions.

Gabe Kangas - [@gabek@social.gabekangas.com](https://social.gabekangas.com/gabek) - email [gabek@real-ity.com](mailto:gabek@real-ity.com)

Project Link: [https://github.com/owncast/owncast](https://github.com/owncast/owncast)
