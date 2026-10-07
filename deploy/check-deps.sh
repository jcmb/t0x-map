#!/usr/bin/env bash
# Verify t0x-map runtime dependencies on the server.
# Usage:
#   ./deploy/check-deps.sh
#   ./deploy/check-deps.sh /etc/t0x-map/config.yaml
set -euo pipefail

CONFIG="${1:-/etc/t0x-map/config.yaml}"

VIEWDAT_PATH="viewdat"
T0X2T0X_PATH="/usr/local/bin/t0x2t0x"
PYTHON_PATH="python3"
DATA_ROOT="/mnt/GPS_Admin/GNSS_Data"
DB_PATH="/var/lib/t0x-map/t0x-map.db"

yaml_get() {
  local key="$1"
  local file="$2"
  # Minimal YAML scalar reader (key: value). Ignores comments / lists.
  awk -v k="$key" '
    $0 ~ "^[[:space:]]*#" { next }
    $1 == k ":" {
      sub(/^[^:]+:[[:space:]]*/, "")
      gsub(/[[:space:]]+#.*$/, "")
      gsub(/^["'\'']|["'\'']$/, "")
      print
      exit
    }
  ' "$file" 2>/dev/null || true
}

if [[ -f "$CONFIG" ]]; then
  echo "Config: $CONFIG"
  v="$(yaml_get viewdat_path "$CONFIG")"
  [[ -n "$v" ]] && VIEWDAT_PATH="$v"
  v="$(yaml_get t0x2t0x_path "$CONFIG")"
  [[ -n "$v" ]] && T0X2T0X_PATH="$v"
  v="$(yaml_get python_path "$CONFIG")"
  [[ -n "$v" ]] && PYTHON_PATH="$v"
  v="$(yaml_get data_root "$CONFIG")"
  [[ -n "$v" ]] && DATA_ROOT="$v"
  v="$(yaml_get db_path "$CONFIG")"
  [[ -n "$v" ]] && DB_PATH="$v"
else
  echo "Config not found ($CONFIG); using defaults from config.example.yaml"
fi

echo
fail=0

ok()   { printf '  OK   %s\n' "$1"; }
bad()  { printf '  FAIL %s\n' "$1"; fail=1; }
warn() { printf '  WARN %s\n' "$1"; }

check_cmd() {
  local label="$1"
  local path="$2"
  local extra="${3:-}"
  if [[ "$path" == */* ]]; then
    if [[ -x "$path" ]]; then
      ok "$label: $path"
      if [[ -n "$extra" ]]; then
        # shellcheck disable=SC2086
        eval "$extra" || warn "$label: ran but exited non-zero"
      fi
    else
      bad "$label: not found or not executable: $path"
    fi
  else
    if command -v "$path" >/dev/null 2>&1; then
      local resolved
      resolved="$(command -v "$path")"
      ok "$label: $resolved"
      if [[ -n "$extra" ]]; then
        eval "$extra" || warn "$label: ran but exited non-zero"
      fi
    else
      bad "$label: not on PATH: $path"
    fi
  fi
}

echo "Tools"
check_cmd "viewdat" "$VIEWDAT_PATH" "\"$VIEWDAT_PATH\" 2>&1 | head -1 >/dev/null"
check_cmd "t0x2t0x" "$T0X2T0X_PATH" "\"$T0X2T0X_PATH\" 2>&1 | head -1 >/dev/null"
check_cmd "python3" "$PYTHON_PATH" "\"$PYTHON_PATH\" -c 'import sys; print(sys.version.split()[0])' >/dev/null"

if command -v t0x-map >/dev/null 2>&1; then
  ok "t0x-map: $(command -v t0x-map) ($(t0x-map version 2>/dev/null || echo '?'))"
else
  bad "t0x-map: not on PATH (/usr/local/bin/t0x-map)"
fi

echo
echo "Paths"
if [[ -d "$DATA_ROOT" ]]; then
  if [[ -r "$DATA_ROOT" ]]; then
    ok "data_root readable: $DATA_ROOT"
  else
    bad "data_root not readable: $DATA_ROOT"
  fi
else
  bad "data_root missing: $DATA_ROOT"
fi

db_dir="$(dirname "$DB_PATH")"
if [[ -d "$db_dir" ]]; then
  if [[ -w "$db_dir" ]]; then
    ok "db directory writable: $db_dir"
  else
    bad "db directory not writable: $db_dir"
  fi
else
  warn "db directory missing (will be created on first run): $db_dir"
fi

echo
if [[ "$fail" -ne 0 ]]; then
  echo "Result: FAILED — fix missing tools/paths before relying on combine / 1s / 30s exports."
  exit 1
fi
echo "Result: OK — required tools look present."
exit 0
