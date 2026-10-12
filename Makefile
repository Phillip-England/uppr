PREFIX ?= $(HOME)/.local

.PHONY: install uninstall test build
install:
	bun install --frozen-lockfile
	bun run build
	mkdir -p "$(PREFIX)/bin" "$(PREFIX)/share/uppr"
	install -m 0644 dist/uppr.js "$(PREFIX)/share/uppr/uppr.js"
	@bun_path="$$(command -v bun)"; \
	  case "$$bun_path" in /*) ;; *) echo "bun must resolve to an absolute path" >&2; exit 1;; esac; \
	  printf '#!/bin/sh\nexec "%s" "%s/share/uppr/uppr.js" "$$@"\n' "$$bun_path" "$(PREFIX)" > "$(PREFIX)/bin/uppr"
	chmod 0755 "$(PREFIX)/bin/uppr"
	@echo "Installed $(PREFIX)/bin/uppr; add $(PREFIX)/bin to PATH if needed."

uninstall:
	rm -f "$(PREFIX)/bin/uppr" "$(PREFIX)/share/uppr/uppr.js"

build:
	bun run build

test:
	bun test
	bun run typecheck
