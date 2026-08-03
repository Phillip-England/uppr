#!/usr/bin/env bash
set -Eeuo pipefail

project_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
service_user=$(id -un)
service_group=$(id -gn)

if [[ ${EUID} -eq 0 ]]; then
	echo "Run reload.sh as the account that should own and operate Uppr, not with sudo." >&2
	echo "The script will request sudo only for systemd installation." >&2
	exit 1
fi

if [[ $(uname -s) != Linux ]] || ! command -v systemctl >/dev/null 2>&1; then
	echo "reload.sh requires Linux with systemd" >&2
	exit 1
fi

if [[ -x "${project_root}/uppr" ]]; then
	uppr_bin="${project_root}/uppr"
elif [[ -x "${HOME}/go/bin/uppr" ]]; then
	uppr_bin="${HOME}/go/bin/uppr"
elif uppr_bin=$(command -v uppr); then
	:
else
	echo "uppr executable not found; build it at ${project_root}/uppr or install it on PATH" >&2
	exit 1
fi

if [[ -n ${CADDYX_PATH:-} && -x ${CADDYX_PATH} ]]; then
	caddyx_bin=${CADDYX_PATH}
elif [[ -x "${project_root}/caddyx" ]]; then
	caddyx_bin="${project_root}/caddyx"
elif [[ -x "${HOME}/.local/bin/caddyx" ]]; then
	caddyx_bin="${HOME}/.local/bin/caddyx"
elif caddyx_bin=$(command -v caddyx); then
	:
else
	echo "caddyx executable not found; run '${uppr_bin} install-caddyx' or set CADDYX_PATH" >&2
	exit 1
fi

staging_dir=$(mktemp -d)
trap 'rm -rf -- "${staging_dir}"' EXIT

echo "Repairing application ownership for ${service_user}:${service_group}..."
sudo chown -R -- "${service_user}:${service_group}" "${project_root}"

echo "Generating Uppr and Caddy service files..."
"${uppr_bin}" service-uppr "${project_root}" > "${staging_dir}/uppr.service"
CADDYX_PATH="${caddyx_bin}" "${uppr_bin}" service-caddy "${project_root}" > "${staging_dir}/caddy.service"

echo "Generating and validating the Caddy configuration..."
"${uppr_bin}" generate-server "${project_root}"
"${caddyx_bin}" validate --config "${project_root}/Caddyfile" --adapter caddyfile

echo "Installing service files and reloading systemd..."
sudo install -o root -g root -m 0644 "${staging_dir}/uppr.service" /etc/systemd/system/uppr.service
sudo install -o root -g root -m 0644 "${staging_dir}/caddy.service" /etc/systemd/system/caddy.service
sudo systemctl daemon-reload
sudo systemctl enable uppr.service caddy.service

echo "Restarting Uppr and Caddy..."
sudo systemctl restart uppr.service
sudo systemctl restart caddy.service

echo "Uppr and Caddy are installed, enabled, and running."
sudo systemctl --no-pager --full status uppr.service caddy.service
