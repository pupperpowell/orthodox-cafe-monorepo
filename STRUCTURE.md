# orthodox.cafe monorepo

This is a monorepo of the new orthodox-cafe web app. It contains a `backend`, written and served with Go, and a `frontend` written in React and Typescript, bundled with Vite, and served with .

**The backend is served with the Go runtime AND written in Go.** (`go run main.go`)

**The frontend has many moving pieces because of course:**

- Vite, a development server and build tool. We will use Vite to bundle for production. It uses `bun` behind the scenes, but is custom-built for development.
- Bun, a Javascript runtime and package manager. It is capable of bundling for production, but it's less advanced than Vite, which focuses on frontend bundling. It is mostly used for running development scripts (see `package.json:{scripts}`)
- Nginx, which serves the bundled and optimized production files.

Bun and Vite do a lot of the same things. Vite is our main weapon for development. Such is the state of web development.

(https://bun.sh/guides/ecosystem/vite)

This is a rewrite of the original orthodox-lofi project, delivering a Byzantine Chanting web radio with dynamic audio processing and ambient audio.

The original implementation used the Web Audio API to handle audio processing on the frontend. This elegant implementation totally ruins audio playback on iOS.

Instead, all audio processing will be done in real time on the backend. There will be five main API endpoints:

- /stream/chanting/inside
- /stream/chanting/outside
- /stream/rain/inside
- /stream/rain/outside
- /stream/ambient (outside only)

## Frontend

The frontend, now React, with Vite and Typescript + SWC as the development environment, will only be responsible for switching between those endpoints based on user preference, volume control, play/pause, and other cute frontend things.

By default, the user will view the page as standing outside the church of St. George with weather and time description. Going inside will switch any required <audio> endpoints.

## Backend

The backend will determine the ambient audio mix based on Eastern Standard Time. In the future, more endpoints may be added.
