#!/bin/bash
# =============================================================================
# Nexra bot (nexrabot, the Go rewrite of the Mirza bot) — install / migrate
#
#   New bot (same variables as the old PHP installer):
#     N=7 TOKEN='123:abc' DOMAIN='bot7.example.com' ADMIN='123456789' \
#     NEXRA_SECRET='secret' CERT_EMAIL='you@example.com' bash install.sh
#
#   Move a running PHP bot to Go (same database, same webhook, no downtime):
#     N=7 bash install.sh migrate          # PHP_DIR=/var/www/html/botmirzapanel7 by default
#
#   Undo a migration (back to PHP exactly as it was):
#     N=7 bash install.sh rollback
#
#   Update every Go bot on this server to the latest release:
#     bash install.sh update
#
#   Show the keys Nexra Panel needs:
#     N=7 bash install.sh keys
#
#   Move EVERY PHP bot on this server (/var/www/html/botmirzapanel*) to Go,
#   one by one, and connect each to Nexra Panel under its owner:
#     curl -sLo /root/nexrabot-install.sh <raw url of this file>
#     NEXRA_PANEL_URL=https://panel.example.com/dashboard NEXRA_PANEL_USER=admin \
#     NEXRA_PANEL_PASS='...' bash /root/nexrabot-install.sh migrate-all
#   A bot that fails is put back on PHP by itself and the others carry on.
#   Without the NEXRA_PANEL_* variables the bots are migrated but not
#   connected; connect them later with:  N=7 bash install.sh register
#
# Optional: NEXRABOT_BIN=/path/to/nexrabot uses a local binary instead of
# downloading the latest release; RELEASE=v6.0.0 pins a release.
# =============================================================================
set -Eeuo pipefail

REPO="${REPO:-MHBehzadian/nexra-mirzabot}"
RELEASE="${RELEASE:-latest}"
BIN=/usr/local/bin/nexrabot
ETC=/etc/nexrabot
BACKUPS=/root/nexrabot-backups
UNIT=/etc/systemd/system/nexrabot@.service

say()  { printf '\033[1;34m>>>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m!!!\033[0m %s\n' "$*"; }
die()  { printf '\033[1;31mxxx\033[0m %s\n' "$*" >&2; exit 1; }

[ "$(id -u)" = 0 ] || die "run as root"

port_for() { echo $((18000 + $1)); }

# ----------------------------------------------------------------- binary

install_binary() {
    if [ -n "${NEXRABOT_BIN:-}" ]; then
        install -m 0755 "$NEXRABOT_BIN" "$BIN.new"
    else
        local arch
        case "$(uname -m)" in
            x86_64|amd64) arch=amd64 ;;
            aarch64|arm64) arch=arm64 ;;
            *) die "unsupported CPU $(uname -m)" ;;
        esac
        local base="https://github.com/$REPO/releases/latest/download"
        [ "$RELEASE" != latest ] && base="https://github.com/$REPO/releases/download/$RELEASE"
        say "downloading nexrabot ($arch) from $base"
        if ! curl -fsSL "$base/nexrabot-linux-$arch" -o "$BIN.new"; then
            # no stable release yet: take the newest one (pre-releases too)
            local tag
            tag=$(curl -fsSL "https://api.github.com/repos/$REPO/releases?per_page=1" | grep -m1 '"tag_name"' | cut -d'"' -f4 || true)
            [ "$RELEASE" = latest ] && [ -n "$tag" ] || die "download failed (is there a release on github.com/$REPO?)"
            base="https://github.com/$REPO/releases/download/$tag"
            say "no stable release; using $tag"
            curl -fsSL "$base/nexrabot-linux-$arch" -o "$BIN.new" || die "download failed"
        fi
        if curl -fsSL "$base/SHA256SUMS" -o /tmp/nexrabot.sums 2>/dev/null; then
            local want got
            want=$(grep " nexrabot-linux-$arch\$" /tmp/nexrabot.sums | awk '{print $1}')
            got=$(sha256sum "$BIN.new" | awk '{print $1}')
            [ -n "$want" ] && [ "$want" != "$got" ] && { rm -f "$BIN.new"; die "checksum mismatch"; }
            rm -f /tmp/nexrabot.sums
        fi
        chmod 0755 "$BIN.new"
    fi
    "$BIN.new" version >/dev/null || { rm -f "$BIN.new"; die "the downloaded binary does not run"; }
    mv -f "$BIN.new" "$BIN"
    say "nexrabot $("$BIN" version) installed at $BIN"
}

install_unit() {
    id nexrabot >/dev/null 2>&1 || useradd --system --home-dir /var/lib/nexrabot --shell /usr/sbin/nologin nexrabot
    mkdir -p "$ETC" "$BACKUPS" /var/lib/nexrabot
    chown nexrabot:nexrabot /var/lib/nexrabot
    chmod 700 "$BACKUPS"
    cat > "$UNIT" <<'EOF'
[Unit]
Description=Nexra Telegram bot %i
After=network-online.target mysql.service mariadb.service
Wants=network-online.target

[Service]
ExecStart=/usr/local/bin/nexrabot serve -c /etc/nexrabot/bot%i.env
User=nexrabot
Group=nexrabot
StateDirectory=nexrabot
Restart=always
RestartSec=2
TimeoutStopSec=30
LimitNOFILE=65536
NoNewPrivileges=yes
ProtectSystem=full
PrivateTmp=yes

[Install]
WantedBy=multi-user.target
EOF
    systemctl daemon-reload
}

secure_config() {
    chown root:nexrabot "$1"
    chmod 0640 "$1"
}

wait_healthy() { # url, seconds — passes only when the Go bot itself answers
    local i
    for i in $(seq 1 "$2"); do
        curl -fsS -m 3 "$1" 2>/dev/null | grep -q '^ok nexrabot' && return 0
        sleep 1
    done
    return 1
}

proxy_snippet() { # N
    cat > "/etc/nginx/snippets/nexrabot$1.conf" <<EOF
# nexrabot $1 (written by install.sh)
location / {
    proxy_pass http://127.0.0.1:$(port_for "$1");
    proxy_http_version 1.1;
    proxy_set_header Host \$host;
    proxy_set_header X-Real-IP \$remote_addr;
    proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto \$scheme;
    proxy_read_timeout 120s;
    client_max_body_size 20m;
}
EOF
}

print_keys() { # N
    echo
    "$BIN" keys -c "$ETC/bot$1.env"
    echo
    echo "Nexra Panel → Bot → افزودن ربات: address https://$(grep '^DOMAIN=' "$ETC/bot$1.env" | cut -d= -f2), and the two keys above."
}

# ----------------------------------------------------------------- Nexra Panel

register_with_panel() { # N — needs NEXRA_PANEL_URL, NEXRA_PANEL_USER, NEXRA_PANEL_PASS
    if [ -z "${NEXRA_PANEL_URL:-}" ] || [ -z "${NEXRA_PANEL_USER:-}" ] || [ -z "${NEXRA_PANEL_PASS:-}" ]; then
        return 0
    fi
    say "connecting bot $1 to Nexra Panel"
    if ! "$BIN" panel-register -c "$ETC/bot$1.env"; then
        warn "bot $1 runs, but connecting it to Nexra Panel failed; retry with: N=$1 bash install.sh register"
        return 1
    fi
}

cmd_register() {
    N="${N:?set N= (bot number)}"
    [ -f "$ETC/bot$N.env" ] || die "$ETC/bot$N.env not found"
    [ -n "${NEXRA_PANEL_URL:-}" ] && [ -n "${NEXRA_PANEL_USER:-}" ] && [ -n "${NEXRA_PANEL_PASS:-}" ] ||
        die "set NEXRA_PANEL_URL, NEXRA_PANEL_USER and NEXRA_PANEL_PASS"
    register_with_panel "$N"
}

cmd_migrate_all() {
    local self ok=() failed=() skipped=() dir n
    self=$(readlink -f "$0")
    [ -f "$self" ] || die "save this script to a file first (curl -sLo /root/nexrabot-install.sh …) and run that file"
    install_binary
    install_unit
    export NEXRABOT_BIN="$BIN" # every bot below uses the binary just installed
    for dir in /var/www/html/botmirzapanel*/; do
        n=${dir%/}; n=${n##*botmirzapanel}
        case "$n" in ''|*[!0-9]*) continue ;; esac
        [ -f "$dir/config.php" ] || continue
        if systemctl is-active --quiet "nexrabot@$n"; then
            say "bot $n already runs on Go"
            register_with_panel "$n" || true
            skipped+=("$n")
            continue
        fi
        echo
        say "======== bot $n ========"
        set +e
        N="$n" PHP_DIR="${dir%/}" bash "$self" migrate 2>&1 | tee "$BACKUPS/migrate-bot$n.log"
        local rc=${PIPESTATUS[0]}
        set -e
        if [ "$rc" = 0 ]; then ok+=("$n"); else failed+=("$n"); fi
    done
    echo
    echo "=========================================="
    echo "moved to Go:        ${ok[*]:-—}"
    echo "already on Go:      ${skipped[*]:-—}"
    echo "failed (still PHP): ${failed[*]:-—}"
    [ ${#failed[@]} -gt 0 ] && echo "logs:               $BACKUPS/migrate-bot<N>.log"
    echo "=========================================="
    [ ${#failed[@]} -eq 0 ]
}

# ----------------------------------------------------------------- new bot

cmd_new() {
    N="${N:?set N= (bot number, e.g. 7)}"
    TOKEN="${TOKEN:?set TOKEN= (bot token from @BotFather)}"
    DOMAIN="${DOMAIN:?set DOMAIN= (domain of this bot, A record must already point here)}"
    ADMIN="${ADMIN:?set ADMIN= (your numeric Telegram id)}"
    NEXRA_SECRET="${NEXRA_SECRET:?set NEXRA_SECRET= (secret code that gates panel management)}"
    CERT_EMAIL="${CERT_EMAIL:?set CERT_EMAIL= (email for the SSL certificate)}"
    DBPASS="${DBPASS:-$(openssl rand -hex 12)}"
    BACKUP_MINUTE="${BACKUP_MINUTE:-$((RANDOM % 60))}"
    local DBNAME="mirzabot${N}" DBUSER="mirza${N}user" CFG="$ETC/bot${N}.env" PORT
    PORT=$(port_for "$N")

    [ -e "$CFG" ] && die "$CFG already exists; bot $N is installed"
    getent hosts "$DOMAIN" >/dev/null || die "DNS for $DOMAIN is not set yet; point the A record here first"
    for t in nginx certbot mysql; do command -v "$t" >/dev/null || die "$t is not installed"; done

    install_binary
    install_unit

    say "resolving the bot username"
    local BOT_USER
    BOT_USER=$(curl -fsS "${NEXRABOT_TELEGRAM_API:-https://api.telegram.org}/bot$TOKEN/getMe" | grep -oP '"username":\s*"\K[^"]+' || true)
    [ -n "$BOT_USER" ] || die "could not resolve the bot username; check the token"

    say "creating database $DBNAME"
    mysql <<EOF
CREATE DATABASE IF NOT EXISTS \`$DBNAME\` CHARACTER SET utf8mb4 COLLATE utf8mb4_general_ci;
CREATE USER IF NOT EXISTS '$DBUSER'@'localhost' IDENTIFIED BY '$DBPASS';
GRANT ALL PRIVILEGES ON \`$DBNAME\`.* TO '$DBUSER'@'localhost';
FLUSH PRIVILEGES;
EOF

    say "writing $CFG"
    NEXRABOT_INIT_DB_PASS="$DBPASS" "$BIN" init-config --out "$CFG" --token "$TOKEN" --admin "$ADMIN" \
        --domain "$DOMAIN" --bot-username "$BOT_USER" --secret "$NEXRA_SECRET" \
        --db-name "$DBNAME" --db-user "$DBUSER" --listen "127.0.0.1:$PORT"
    secure_config "$CFG"
    "$BIN" schema -c "$CFG" >/dev/null
    "$BIN" check -c "$CFG"

    say "starting nexrabot@$N"
    systemctl enable --now "nexrabot@$N"
    wait_healthy "http://127.0.0.1:$PORT/healthz" 20 || die "the bot did not start; see: journalctl -u nexrabot@$N"

    say "nginx site for $DOMAIN"
    proxy_snippet "$N"
    cat > "/etc/nginx/sites-available/bot${N}" <<EOF
server {
    listen 80;
    server_name $DOMAIN;
    include /etc/nginx/snippets/nexrabot${N}.conf;
}
EOF
    ln -sf "/etc/nginx/sites-available/bot${N}" "/etc/nginx/sites-enabled/bot${N}"
    nginx -t && systemctl reload nginx

    say "SSL certificate"
    certbot --nginx -d "$DOMAIN" --agree-tos --redirect --no-eff-email -m "$CERT_EMAIL"
    wait_healthy "https://$DOMAIN/healthz" 20 || die "https://$DOMAIN/healthz does not answer"

    say "webhook"
    "$BIN" set-webhook -c "$CFG"

    say "hourly database backup to your Telegram"
    cat > "/root/bot${N}_backup.sh" <<EOF
#!/bin/bash
FILE="/root/mirzabot${N}_\$(date +%Y%m%d_%H%M%S).sql.gz"
$BIN db-dump -c $CFG --out "\$FILE" >/dev/null
curl -s -F chat_id="$ADMIN" -F document=@"\$FILE" -F caption="بکاپ خودکار $BOT_USER 🗄" \\
  "${NEXRABOT_TELEGRAM_API:-https://api.telegram.org}/bot$TOKEN/sendDocument" >/dev/null
rm -f "\$FILE"
EOF
    chmod 700 "/root/bot${N}_backup.sh"
    (crontab -l 2>/dev/null | grep -v "bot${N}_backup"; echo "$BACKUP_MINUTE * * * * bash /root/bot${N}_backup.sh") | crontab -

    echo
    echo "=========================================="
    echo "done: @$BOT_USER on https://$DOMAIN"
    echo "database: $DBNAME / $DBUSER / $DBPASS"
    print_keys "$N"
    echo "=========================================="
}

# ----------------------------------------------------------------- migrate a PHP bot

nginx_site_for() { # php dir -> site file that serves it with php-fpm
    local f
    for f in /etc/nginx/sites-enabled/* /etc/nginx/conf.d/*.conf; do
        [ -f "$f" ] || continue
        if grep -Eq "^\s*root\s+${1%/}/?;" "$f" && grep -q "fastcgi_pass" "$f"; then
            readlink -f "$f"
            return 0
        fi
    done
    return 1
}

# Replace "location / {...}" with the proxy include and drop the php-fpm
# location, in every server block of the site file (certbot copies them into
# the 443 block). Only the flat blocks install.sh/certbot write are handled;
# anything else aborts before nginx is touched.
rewrite_nginx_site() { # file N
    local out="$1.nexrabot.tmp"
    N_SNIPPET="/etc/nginx/snippets/nexrabot$2.conf" perl -0777 -pe '
        my $inc = $ENV{N_SNIPPET};
        my $n = s{location\s+/\s*\{[^{}]*\}}{include $inc;}g;
        s{location\s+~\s+\\\.php\$\s*\{[^{}]*\}\s*}{}g;
        die "no \"location /\" block\n" unless $n;
    ' "$1" > "$out" || { rm -f "$out"; return 1; }
    grep -q "fastcgi_pass" "$out" && { rm -f "$out"; warn "fastcgi_pass left in the site file"; return 1; }
    mv -f "$out" "$1"
}

cmd_migrate() {
    N="${N:?set N= (bot number)}"
    local PHP_DIR="${PHP_DIR:-/var/www/html/botmirzapanel${N}}"
    PHP_DIR="${PHP_DIR%/}"
    local CFG="$ETC/bot${N}.env" PORT TS SITE DOMAIN
    PORT=$(port_for "$N")
    TS=$(date +%Y%m%d_%H%M%S)

    [ -f "$PHP_DIR/config.php" ] || die "$PHP_DIR/config.php not found (set PHP_DIR=)"
    command -v nginx >/dev/null || die "nginx is not installed"
    SITE=$(nginx_site_for "$PHP_DIR") || die "no nginx site serves $PHP_DIR with php-fpm; migrate this one by hand (see docs/MIGRATION.md)"
    # dry-run the nginx rewrite on a copy, so an unusual layout stops us here
    cp "$SITE" "/tmp/nexrabot-site-check.$$"
    rewrite_nginx_site "/tmp/nexrabot-site-check.$$" "$N" || { rm -f "/tmp/nexrabot-site-check.$$"; die "$SITE has a layout this script can't rewrite safely; migrate by hand (see docs/MIGRATION.md)"; }
    rm -f "/tmp/nexrabot-site-check.$$"
    if systemctl is-active --quiet "nexrabot@$N"; then die "nexrabot@$N is already running; nothing to migrate"; fi
    if ss -ltn 2>/dev/null | grep -q ":$PORT "; then die "port $PORT is already in use"; fi

    install_binary
    install_unit

    say "reading $PHP_DIR/config.php"
    "$BIN" migrate-php --php-dir "$PHP_DIR" --out "$CFG" --listen "127.0.0.1:$PORT" --skip-db
    secure_config "$CFG"
    DOMAIN=$(grep '^DOMAIN=' "$CFG" | cut -d= -f2)
    case "$DOMAIN" in */*) die "the PHP bot runs under a path ($DOMAIN); migrate it by hand (see docs/MIGRATION.md)";; esac

    mkdir -p "$BACKUPS"
    say "backing up the database (before anything changes)"
    "$BIN" db-dump -c "$CFG" --out "$BACKUPS/bot${N}-${TS}.sql.gz"

    say "checking the database and the token"
    "$BIN" check -c "$CFG"

    say "upgrading the schema (adds only what is missing; the PHP bot keeps working)"
    "$BIN" migrate-php --php-dir "$PHP_DIR" --out "$CFG" --listen "127.0.0.1:$PORT" --clean-crontab=false
    secure_config "$CFG"

    say "trial run on 127.0.0.1:$PORT (crons off)"
    runuser -u nexrabot -- env NEXRABOT_DISABLE_CRONS=1 "$BIN" serve -c "$CFG" > "$BACKUPS/bot${N}-${TS}.trial.log" 2>&1 &
    local TRIAL=$!
    if ! wait_healthy "http://127.0.0.1:$PORT/healthz" 20; then
        kill "$TRIAL" 2>/dev/null || true
        cat "$BACKUPS/bot${N}-${TS}.trial.log"
        die "the Go bot did not start; the PHP bot was not touched"
    fi
    kill "$TRIAL"; wait "$TRIAL" 2>/dev/null || true

    # ---- cut over: from here on, any failure puts PHP back
    cp -a "$SITE" "$ETC/bot${N}.nginx.bak"
    echo "$SITE" > "$ETC/bot${N}.nginx.path"
    crontab -l > "$ETC/bot${N}.crontab.bak" 2>/dev/null || true
    trap 'warn "migration failed — restoring the PHP bot"; restore_php "$N"; exit 1' ERR

    say "stopping the PHP bot's cron jobs (the Go bot runs them itself)"
    grep -v -F "$DOMAIN/cron/" "$ETC/bot${N}.crontab.bak" | crontab - || true

    say "starting nexrabot@$N"
    systemctl enable --now "nexrabot@$N"
    wait_healthy "http://127.0.0.1:$PORT/healthz" 20

    say "switching nginx from php-fpm to the Go bot"
    proxy_snippet "$N"
    rewrite_nginx_site "$SITE" "$N"
    nginx -t
    systemctl reload nginx
    wait_healthy "https://$DOMAIN/healthz" 20

    trap - ERR
    say "securing the webhook with a secret token"
    "$BIN" set-webhook -c "$CFG" || warn "set-webhook failed; the bot still works (updates are accepted from Telegram's addresses)"

    register_with_panel "$N" || true

    # the hourly backup script of the PHP installer keeps working as it is
    echo
    echo "=========================================="
    echo "bot $N now runs on Go: https://$DOMAIN"
    echo "database backup: $BACKUPS/bot${N}-${TS}.sql.gz"
    echo "logs:            journalctl -u nexrabot@$N -f"
    echo "undo:            N=$N bash install.sh rollback"
    print_keys "$N"
    echo "=========================================="
}

restore_php() { # N
    local N="$1" SITE DOMAIN i
    DOMAIN=$(grep '^DOMAIN=' "$ETC/bot${N}.env" | cut -d= -f2)
    # nginx first, so requests go straight from Go to PHP with no gap
    if [ -f "$ETC/bot${N}.nginx.bak" ] && [ -f "$ETC/bot${N}.nginx.path" ]; then
        SITE=$(cat "$ETC/bot${N}.nginx.path")
        cp -a "$ETC/bot${N}.nginx.bak" "$SITE"
        nginx -t && systemctl reload nginx
        for i in $(seq 1 15); do
            curl -fsS -m 3 "https://$DOMAIN/healthz" 2>/dev/null | grep -q '^ok nexrabot' || break
            sleep 1
        done
    fi
    systemctl disable --now "nexrabot@$N" 2>/dev/null || true
    if [ -f "$ETC/bot${N}.crontab.bak" ]; then
        # put back the PHP bot's cron lines, keep everything added since
        { crontab -l 2>/dev/null | grep -v -F "$DOMAIN/cron/" || true; grep -F "$DOMAIN/cron/" "$ETC/bot${N}.crontab.bak" || true; } | crontab -
    fi
}

cmd_rollback() {
    N="${N:?set N= (bot number)}"
    [ -f "$ETC/bot${N}.nginx.bak" ] || die "no migration backup for bot $N in $ETC"
    say "putting bot $N back on PHP"
    restore_php "$N"
    # Telegram keeps sending to the same URL; a webhook secret is simply ignored by PHP.
    say "done. The PHP bot serves https://$(grep '^DOMAIN=' "$ETC/bot${N}.env" | cut -d= -f2) again with the same database."
    echo "To try the migration again later: N=$N bash install.sh migrate"
}

# ----------------------------------------------------------------- update

cmd_update() {
    local units
    units=$(systemctl list-units --type=service --all --plain --no-legend 'nexrabot@*' | awk '{print $1}')
    install_binary
    install_unit
    for u in $units; do
        local n=${u#nexrabot@}; n=${n%.service}
        say "restarting $u"
        systemctl restart "$u"
        wait_healthy "http://127.0.0.1:$(port_for "$n")/healthz" 30 || warn "$u is not healthy; see: journalctl -u $u"
    done
    say "all bots run $("$BIN" version)"
}

case "${1:-new}" in
    new) cmd_new ;;
    migrate) cmd_migrate ;;
    rollback) cmd_rollback ;;
    update) cmd_update ;;
    keys) N="${N:?set N=}"; print_keys "$N" ;;
    register) cmd_register ;;
    migrate-all) cmd_migrate_all ;;
    *) die "usage: install.sh [new|migrate|migrate-all|register|rollback|update|keys]" ;;
esac
