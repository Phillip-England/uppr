package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func dumpCommand(args []string) error {
	if len(args) > 1 {
		return errors.New("usage: uppr dump [directory]")
	}
	root := "."
	if len(args) == 1 {
		root = args[0]
	}
	path, err := filepath.Abs(filepath.Join(root, "UPPR.md"))
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(integrationGuide), 0o644); err != nil {
		return err
	}
	fmt.Println("wrote", path)
	return nil
}

const integrationGuide = `# Integrating this application with Uppr v2

Uppr runs this repository as one independently managed Docker container. Adapt the application to the following contract.

## Repository files

- Commit a production Dockerfile at the repository root. Its process must remain in the foreground and listen on 0.0.0.0.
- Commit schema.json at the repository root. Declare every environment variable the app needs.
- Ignore /config/ and /data/ in .gitignore and exclude them from the Docker build context in .dockerignore.
- Never commit real credentials, databases, or uploads.

Example schema.json:

~~~json
{
  "variables": [
    {
      "name": "API_KEY",
      "description": "Token for the upstream API",
      "example": "a token from the provider dashboard",
      "required": true
    }
  ]
}
~~~

## Runtime layout

Uppr creates config/.env and data/main.sqlite under the repository. Inside the container, it bind mounts those directories to /app/config and /app/data and passes /app/config/.env through Docker's --env-file option. Read configuration from process environment variables. Store SQLite at /app/data/main.sqlite and all other durable state under /app/data. Do not depend on mutable files elsewhere in the image.

The container port is set in the launchpad manifest when the repository is added. Ensure the app listens on that port. Uppr publishes it on a distinct host loopback port and Caddy proxies the configured domain to that port.

## Check before shipping

1. Build the image with docker build.
2. Start it with empty config/ and data/ directories mounted at /app/config and /app/data.
3. Verify it uses the expected container port and persists data after the container is replaced.
4. Verify config/ and data/ are ignored by Git and Docker.
5. Verify every required variable in schema.json is used and documented accurately.
`
