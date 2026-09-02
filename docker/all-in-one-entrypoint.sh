#!/bin/sh
set -eu

# RDS_* is the public configuration namespace. ART_* remains an internal
# compatibility namespace for one migration cycle so existing installations
# can upgrade without rotating secrets or rebuilding their environment lists.
map_env() {
    new_name="$1"
    old_name="$2"
    new_value="$(printenv "$new_name" 2>/dev/null || true)"
    old_value="$(printenv "$old_name" 2>/dev/null || true)"
    if [ -n "$new_value" ]; then
        export "$old_name=$new_value"
    elif [ -n "$old_value" ]; then
        export "$new_name=$old_value"
    fi
}

for suffix in DATA_DIR DB_DRIVER DB_DSN BACKUP_INTERVAL BACKUP_RETENTION API_LISTEN API_INTERNAL_URL JWT_SECRET JWT_SECRET_FILE INTERNAL_SECRET INTERNAL_SECRET_FILE BUILDER_TOKEN BUILDER_TOKEN_FILE MFA_SECRET MFA_SECRET_FILE MFA_MODE BOOTSTRAP_SECRET_FILE BOOTSTRAP_ADMIN_USERNAME BOOTSTRAP_ADMIN_PASSWORD ACCESS_TOKEN_TTL SESSION_TTL LOGIN_BURST LOGIN_WINDOW LOGIN_LOCKOUT REGISTRATION_ENABLED REGISTRATION_AUTO_APPROVE REQUIRE_LOGIN REQUIRE_DEVICE_DEPLOYMENT RELAY_SERVER DEVICE_ONLINE_TTL AUTH_CACHE_FILE AUTH_RECONCILE_SECONDS SERVER_KEY_FILE HBBS_TCP_LISTEN HBBS_UDP_LISTEN HBBS_NAT_LISTEN HBBS_WEBSOCKET_LISTEN HBBS_ID HBBR_LISTEN HBBR_WEBSOCKET_LISTEN HBBR_CONTROL_LISTEN HBBR_CONTROL_ADDRESS HBBR_ID HBBR_NAME HBBR_REGION HBBR_PUBLIC_ADDRESS HBBR_TELEMETRY_INTERVAL SERVICE_HEARTBEAT_INTERVAL OIDC_ISSUER OIDC_CLIENT_ID OIDC_CLIENT_SECRET OIDC_REDIRECT_URL OIDC_PROVIDER_NAME OIDC_SCOPES OIDC_AUTO_REGISTER LDAP_URL LDAP_BIND_DN LDAP_BIND_PASSWORD LDAP_BASE_DN LDAP_USER_FILTER LDAP_USERNAME_ATTRIBUTE LDAP_EMAIL_ATTRIBUTE LDAP_DISPLAY_NAME_ATTRIBUTE LDAP_GROUP_ATTRIBUTE LDAP_GROUP_MAPPING LDAP_STARTTLS LDAP_INSECURE_SKIP_VERIFY LDAP_AUTO_PROVISION WEBHOOK_ALLOW_PRIVATE; do
    map_env "RDS_$suffix" "ART_$suffix"
done

data_dir="${RDS_DATA_DIR:-${ART_DATA_DIR:-/data}}"
database_driver="${RDS_DB_DRIVER:-${ART_DB_DRIVER:-sqlite}}"
database_path="${RDS_DB_DSN:-${ART_DB_DSN:-$data_dir/art.db}}"
restore_dir="$data_dir/backups"
restore_candidate="$restore_dir/restore.pending.db"
restore_marker="$restore_dir/restore.pending.sha256"

if [ -f "$restore_marker" ]; then
  if [ "$database_driver" != "sqlite" ]; then
    echo "A staged restore can only be applied to SQLite" >&2
    exit 1
  fi
  case "$database_path" in /*) ;; *) echo "A staged restore requires an absolute SQLite database path" >&2; exit 1 ;; esac
  case "$database_path" in *'?'*) echo "Unsafe SQLite database path for staged restore" >&2; exit 1 ;; esac
  if [ ! -f "$restore_candidate" ]; then
    echo "Restore marker exists but the staged database is missing" >&2
    exit 1
  fi
  restore_checksum="$(sed -n '1p' "$restore_marker")"
  if ! printf '%s' "$restore_checksum" | grep -Eq '^[0-9a-f]{64}$'; then
    echo "Invalid staged restore checksum" >&2
    exit 1
  fi
  if ! (cd "$restore_dir" && printf '%s  restore.pending.db\n' "$restore_checksum" | sha256sum -c -); then
    echo "Staged restore checksum verification failed" >&2
    exit 1
  fi
  recovery_dir="$restore_dir/pre-restore-$(date -u +%Y%m%d-%H%M%S)"
  mkdir -m 700 "$recovery_dir"
  for current_file in "$database_path" "$database_path-wal" "$database_path-shm"; do
    if [ -f "$current_file" ]; then cp "$current_file" "$recovery_dir/"; fi
  done
  cp "$restore_candidate" "$database_path.restore.tmp"
  chmod 600 "$database_path.restore.tmp"
  mv "$database_path.restore.tmp" "$database_path"
  rm -f "$database_path-wal" "$database_path-shm" "$restore_candidate" "$restore_marker"
  printf 'restored_at=%s\nrecovery_dir=%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$recovery_dir" > "$restore_dir/restore.last"
  chmod 600 "$restore_dir/restore.last"
  echo "Validated SQLite restore applied; previous database files saved in $recovery_dir"
fi

art-api &
api_pid=$!

attempt=0
while [ ! -s "${RDS_JWT_SECRET_FILE:-${ART_JWT_SECRET_FILE:-/data/secrets/jwt.secret}}" ] || [ ! -s "${RDS_INTERNAL_SECRET_FILE:-${ART_INTERNAL_SECRET_FILE:-/data/secrets/internal.secret}}" ]; do
  attempt=$((attempt + 1))
  if [ "$attempt" -gt 100 ] || ! kill -0 "$api_pid" 2>/dev/null; then
    echo "RustDesk Server RouterOS API did not initialize shared secrets" >&2
    exit 1
  fi
  sleep 0.1
done

art-hbbr &
hbbr_pid=$!
art-hbbs &
hbbs_pid=$!

shutdown() {
  kill "$hbbs_pid" "$hbbr_pid" "$api_pid" 2>/dev/null || true
  wait "$hbbs_pid" "$hbbr_pid" "$api_pid" 2>/dev/null || true
}
trap shutdown INT TERM EXIT

while kill -0 "$api_pid" 2>/dev/null && kill -0 "$hbbs_pid" 2>/dev/null && kill -0 "$hbbr_pid" 2>/dev/null; do
  sleep 2
done
exit 1
