#!/usr/bin/env bash
# Marzban subscription patch: when a user's data runs out (limited) or their time
# runs out (expired), every config except the first is renamed, alternating a
# Persian and an English notice. The names are built on each subscription request,
# so once the user is renewed (status back to active) a sub update restores them.
#
#   install / re-install (run again after every Marzban update):
#     bash expired-remark.sh
#   remove:
#     bash expired-remark.sh uninstall
set -euo pipefail

APP_DIR="${APP_DIR:-/opt/marzban}"
COMPOSE_FILE="$APP_DIR/docker-compose.yml"
CUSTOM_DIR="$APP_DIR/custom"
PATCHED="$CUSTOM_DIR/share.py"
TARGET="/code/app/subscription/share.py"
MOUNT_LINE="$PATCHED:$TARGET:ro"

red()   { printf '\033[31m%s\033[0m\n' "$*"; }
green() { printf '\033[32m%s\033[0m\n' "$*"; }
die()   { red "ERROR: $*"; exit 1; }

[ "$(id -u)" -eq 0 ] || die "run as root"
[ -f "$COMPOSE_FILE" ] || die "$COMPOSE_FILE not found (set APP_DIR=... if Marzban lives elsewhere)"

if docker compose version >/dev/null 2>&1; then DC=(docker compose -f "$COMPOSE_FILE" --project-directory "$APP_DIR")
elif command -v docker-compose >/dev/null 2>&1; then DC=(docker-compose -f "$COMPOSE_FILE" --project-directory "$APP_DIR")
else die "docker compose not found"; fi

has_mount() { grep -qF "$TARGET" "$COMPOSE_FILE"; }

remove_mount() {
    if has_mount; then
        sed -i "\#$TARGET#d" "$COMPOSE_FILE"
    fi
}

recreate() {
    "${DC[@]}" up -d --force-recreate marzban >/dev/null
}

wait_healthy() {
    # give Marzban time to boot; a broken share.py makes it crash-loop
    sleep 15
    local cid status restarts
    cid="$("${DC[@]}" ps -q marzban)"
    [ -n "$cid" ] || return 1
    status="$(docker inspect -f '{{.State.Status}}' "$cid")"
    restarts="$(docker inspect -f '{{.RestartCount}}' "$cid")"
    [ "$status" = "running" ] && [ "$restarts" = "0" ]
}

if [ "${1:-}" = "uninstall" ]; then
    remove_mount
    rm -f "$PATCHED"
    recreate
    green "Patch removed, Marzban restarted with its original share.py."
    exit 0
fi

CID="$("${DC[@]}" ps -q marzban)"
[ -n "$CID" ] || die "the marzban container is not running; start it first (marzban up)"
IMAGE="$(docker inspect -f '{{.Image}}' "$CID")"

mkdir -p "$CUSTOM_DIR"
cp "$COMPOSE_FILE" "$COMPOSE_FILE.bak.$(date +%s)"

# Patch the pristine share.py from the running image (never from an older patched copy).
docker run --rm -i -v "$CUSTOM_DIR:/out" --entrypoint python3 "$IMAGE" - <<'PY'
import re, sys

src = open("/code/app/subscription/share.py", encoding="utf-8").read()

loop = re.compile(r"^(?P<ind>[ \t]*)for protocol, tags in inbounds:[ \t]*$", re.M)
remark = re.compile(r'remark=host\["remark"\]\.format_map\(format_variables\),')

if len(loop.findall(src)) != 1 or len(remark.findall(src)) != 1:
    sys.exit("share.py of this Marzban version has an unexpected layout; patch not applied")

src = loop.sub(
    lambda m: (
        f'{m["ind"]}_nx_state = {{"i": 0, "msgs": _nx_messages(format_variables)}}\n'
        f'{m["ind"]}for protocol, tags in inbounds:'
    ),
    src,
)
src = remark.sub(
    lambda m: 'remark=_nx_remark(_nx_state, host["remark"].format_map(format_variables)),',
    src,
)

src += '''

# ---- nexra expired-remark patch ----
NX_REMARKS = {
    "limited": (
        "کاربر گرامی حجم اشتراک شما به پایان رسیده است",
        "Dear user, your subscription data has run out",
    ),
    "expired": (
        "کاربر گرامی زمان اشتراک شما به پایان رسیده است",
        "Dear user, your subscription has expired",
    ),
}


def _nx_messages(format_variables):
    emojis = globals().get("STATUS_EMOJIS", {})
    status = {v: k for k, v in emojis.items()}.get(format_variables.get("STATUS_EMOJI"))
    return NX_REMARKS.get(status)


def _nx_remark(state, original):
    i = state["i"]
    state["i"] += 1
    msgs = state["msgs"]
    if not msgs or i == 0:
        return original
    return msgs[(i - 1) % 2]
'''

compile(src, "share.py", "exec")
open("/out/share.py", "w", encoding="utf-8").write(src)
print("share.py patched")
PY

[ -s "$PATCHED" ] || die "patched file was not written"

if ! has_mount; then
    anchor='^[[:space:]]*-[[:space:]]*/var/lib/marzban:/var/lib/marzban[[:space:]]*$'
    grep -qE "$anchor" "$COMPOSE_FILE" || die "could not find the '/var/lib/marzban:/var/lib/marzban' volume line in $COMPOSE_FILE; add this volume to the marzban service manually: $MOUNT_LINE"
    awk -v anchor="$anchor" -v mount="$MOUNT_LINE" '
        { print }
        !done && $0 ~ anchor {
            match($0, /^[ \t]*/)
            print substr($0, 1, RLENGTH) "- " mount
            done = 1
        }' "$COMPOSE_FILE" > "$COMPOSE_FILE.tmp" && mv "$COMPOSE_FILE.tmp" "$COMPOSE_FILE"
fi

recreate
if wait_healthy; then
    green "Done. Limited/expired users now see the notice after updating their subscription."
    green "Run this script again after every 'marzban update'."
else
    red "Marzban did not come up cleanly with the patch; rolling back..."
    remove_mount
    recreate
    die "rolled back to the original share.py; check: marzban logs"
fi
