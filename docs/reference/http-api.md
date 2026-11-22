# Web API

The web server has a small JSON API for its own page. It only answers requests from the same computer. To
call it with `curl`, set the `Host` header:

    curl -s -H 'Host: gcse-revision.localhost:8765' \
      http://127.0.0.1:8765/api/info

It returns:

    {"app":"gcse-revision","version":"v0.12.3"}

The full specification is in [`openapi.yaml`](../api/openapi.yaml).

## Endpoints

- `GET /api/info`: The app name and version.
- `GET /api/llm/status`: The AI helper's state (see below).
- `POST /api/llm/setup`: Starts the AI helper set-up.
- `GET /api/llm/builtin`: Whether Apple Intelligence is available.
- `POST /llm/builtin/chat/completions`: A reply from Apple Intelligence.
- `/llm/…`: Passed straight to Ollama, e.g. `/llm/api/tags` reaches Ollama's `/api/tags`.
- `POST /api/import`: Imports a pack sent as the request body (up to 32 MB).
- `GET /subjects.js`: The list of subjects.
- `GET /data/<name>.js`: One subject pack.
- `GET /audio/<pack>/<file>`: A recording from a private pack.
- `GET /`: The web page.

## AI helper status

    {
      "phase": "ready",
      "message": "Ready — using llama3.2:latest.",
      "progress": 1,
      "model": "llama3.2:latest",
      "backend": "Ollama",
      "proxy": "/llm"
    }

- `idle`: Not started.
- `disabled`: Switched off with `--no-ai`.
- `checking`, `starting`: Looking for or starting Ollama.
- `installing`, `pulling`: Downloading. `progress` runs from 0 to 1.
- `ready`: Ready to use.
- `error`: Failed. `message` explains why.

## Errors

Import and Apple Intelligence errors come back as JSON:

    {
      "error": "bundle: not valid JSON: invalid character 'o' …"
    }

Other errors are plain text.

- `400`: The request body was invalid.
- `403`: The request came from another website or address.
- `404`: Not found.
- `405`: Wrong method, e.g. `GET` where `POST` is needed.
- `502`: Ollama isn't running yet.
- `503`: Apple Intelligence isn't available.
