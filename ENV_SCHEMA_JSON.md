# Uppr schema.json

Apps managed by Uppr can declare required runtime environment variables in a
`schema.json` file at the repository root. Uppr reads this file after pull/sync
and uses it to prepare `config/.env` with missing keys, while preserving any
existing values.

## Location

Place the file here:

```text
schema.json
```

## Format

Use a top-level `variables` array. Each variable needs a `name`. Descriptions
and examples are shown in the Uppr UI when someone fills out the app
configuration.

```json
{
  "variables": [
    {
      "name": "DATABASE_URL",
      "description": "Postgres connection string used by the application.",
      "example": "postgres://user:password@db:5432/app",
      "required": true
    },
    {
      "name": "API_KEY",
      "description": "Secret token used to call the upstream API.",
      "example": "sk_live_...",
      "required": true
    }
  ]
}
```

## Rules

- `name` must be a valid environment variable name such as `API_KEY`,
  `DATABASE_URL`, or `SESSION_SECRET`.
- `description` should explain what the value is for and where to get it.
- `example` should show the expected shape without exposing a real secret.
- `required` defaults to `true` when omitted.
- Do not include real passwords, tokens, or private keys in `schema.json`.

Uppr still supports the older `env.schema` format with one variable name per
line, but `schema.json` is preferred because it carries setup guidance into the
UI.
