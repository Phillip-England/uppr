# TRAIT.md

This project uses the trait system to extract reusable implementation patterns from a codebase. A trait is a concise markdown document that describes one repeatable behavior, architecture choice, integration pattern, UI pattern, security control, deployment convention, or developer workflow found in the project.

When an LLM is asked to inspect this project for traits, it should read this file first, inspect the repository, then create a `./traits` directory containing one markdown file per trait it finds.

## What Counts As A Trait

A good trait is:

- Specific enough to reuse in another project.
- Grounded in real files, commands, configuration, or behavior found in this repository.
- Written as implementation guidance, not as a changelog or generic documentation.
- Small enough that one trait describes one coherent pattern.
- Useful to a future LLM or engineer trying to reproduce the same pattern elsewhere.

Examples of traits include:

- Persistent SQLite storage under a project-owned data directory.
- Docker and Makefile commands that mount runtime directories.
- Admin authentication with IP-based rate limiting.
- A mobile navigation drawer with accessible controls.
- An explicit environment configuration schema.
- A startup routine that creates required runtime directories.

Do not create traits for one-off details that are not reusable, generated files, vendored dependencies, build artifacts, or secrets.

## Trait File Format

Create traits in `./traits`. Each trait should be a markdown file named with a lowercase kebab-case slug:

~~~text
traits/persistent-sqlite-data-directory.md
traits/mobile-navigation-drawer.md
traits/docker-runtime-mounts.md
~~~

Each trait must start with YAML front matter. Put reusable hashtags in the `hashtags` list. Hashtags must include the leading `#` character so an admin portal can ingest them directly.

~~~markdown
---
title: Persistent SQLite Data Directory
hashtags:
  - "#sqlite"
  - "#database"
  - "#persistence"
  - "#docker"
---

# Persistent SQLite Data Directory

## Intent

Describe the reusable pattern in one or two short paragraphs.

## When To Use

Explain the project situations where this trait applies.

## Implementation

Describe the concrete implementation. Include relevant paths, commands, environment variables, schema fields, handlers, components, or configuration names.

## Project Evidence

Reference the files or directories that prove this trait exists in the project.

## Reuse Notes

Explain what another project should copy, adapt, or avoid.
~~~

Keep the first `# Heading` aligned with the front matter title. Use additional markdown hashtags in the body only when they are useful; the canonical tags live in front matter.

## Inspection Workflow For An LLM

1. Read `TRAIT.md` completely.
2. Inspect the project tree with a fast file search.
3. Read the main application entry points, configuration files, Docker files, package manifests, database setup, UI components, and tests.
4. Identify repeated or reusable implementation patterns.
5. Create `./traits` if it does not exist.
6. Write one markdown file per discovered trait using the format above.
7. Keep every trait grounded in project evidence.
8. Do not include secrets, credentials, generated databases, local runtime files, dependency directories, or private machine paths.

## Quality Bar

Each trait should teach a future implementation. Prefer concrete details over broad claims:

- Use exact file and directory names when they matter.
- Name environment variables and commands.
- Describe startup behavior and failure modes.
- Mention security boundaries and persistence boundaries.
- Include enough detail for another LLM to reproduce the pattern without rereading the whole project.

Avoid vague traits such as "uses Go" or "has a website" unless the project contains a distinctive reusable pattern around that technology.
