#!/usr/bin/env bash
# maxgw-update.sh — keep this fork on top of upstream Coddy, one step at a time.
#
# Our branch rides on upstream: `main` is our work as a rebase-able overlay on
# `upstream/main` (the branch `upstream` keeps the untouched tree). docs/MAX-OVERLAY.md says which of our changes touch a
# file upstream also changes, and why — that is the map used when a rebase conflicts.
#
#   --check     what is new upstream, and how many files would conflict (exit 1 when behind)
#   --dry       rehearse the rebase in a throwaway worktree; the working tree is untouched
#   --apply     do the rebase; on a conflict it stops and names the files
#   --build     gofmt, vet, the release build and the adapter tests
#   --install   back up the installed binary, stop the service, replace it, start it again;
#               rolls back on its own if the port stays silent. MAXGW_INSTALL_DRY=1 prints
#               the whole plan and touches nothing.
#   --version   the version string the next build carries
#
# --check and --dry are safe unattended. --apply, --build and --install change the tree or
# the machine, so they are for a person (or an agent acting on that person's word).
set -uo pipefail

UPSTREAM_REMOTE="${MAXGW_UPSTREAM_REMOTE:-upstream}"
UPSTREAM_BRANCH="${MAXGW_UPSTREAM_BRANCH:-main}"
OUR_BRANCH="${MAXGW_BRANCH:-main}"
# browser is in the default set: this machine's configuration carries browser.enable: true, and
# Coddy refuses to start when the configuration enables a surface the binary cannot run
# (10.11.2026: a build without it left both bots down for 22 minutes).
BUILD_TAGS="${MAXGW_TAGS:-http,ui,scheduler,memory,cli,gateway,swarm,browser}"
ADAPTER_PKG="./external/gateway/max/"
BIN_DIR="${MAXGW_BIN_DIR:-$HOME/.local/bin}"
START_SCRIPT="${MAXGW_START_SCRIPT:-$HOME/start_coddy.sh}"
BIN_NAME=coddy
# The service writes its stdout where the start script sends it; the path is a setting, not a
# constant, because this script ships to other machines.
STDOUT_LOG="${MAXGW_STDOUT_LOG:-$HOME/coddy/logs/coddy-stdout.log}"

say() { printf '%s\n' "$*"; }
die() { printf 'error: %s\n' "$*" >&2; exit 2; }

# check_subsystems refuses a build that cannot run the configuration this machine carries.
# Coddy names the gap itself - "browser.enable is true but this binary has no browser support
# (rebuild with -tags browser)" - and exits, so asking the candidate binary before the running
# service is replaced is the cheapest possible guard. A port already in use is the running
# service, not a fault of the build, so only the missing-subsystem error counts.
check_subsystems() {
	local bin="$1" out
	[ -x "$bin" ] || die "cannot check $bin: not executable"
	out=$(CODDY_HOME="${CODDY_HOME:-$HOME/.coddy}" "$bin" serve --dry-run --test-config 2>&1)
	if printf '%s' "$out" | grep -q 'rebuild with -tags'; then
		printf '%s\n' "$out" | grep -E 'rebuild with -tags' | sed 's/^[[:space:]]*/  /' >&2
		die "this build misses a subsystem the configuration enables; nothing was installed"
	fi
	return 0
}

# DRY_WORKTREE is the throwaway worktree --dry rehearses in. An EXIT trap cannot read a
# local variable, so the path lives here and cleanup_dry clears it once the tree is gone.
DRY_WORKTREE=""
cleanup_dry() {
	[ -n "$DRY_WORKTREE" ] || return 0
	git worktree remove --force "$DRY_WORKTREE" >/dev/null 2>&1
	DRY_WORKTREE=""
}

cd "$(git rev-parse --show-toplevel 2>/dev/null)" || die "not inside a git repository"
git rev-parse --verify --quiet "$UPSTREAM_REMOTE/$UPSTREAM_BRANCH" >/dev/null ||
	die "no $UPSTREAM_REMOTE/$UPSTREAM_BRANCH: git remote add $UPSTREAM_REMOTE https://github.com/coddy-project/coddy-agent.git && git fetch $UPSTREAM_REMOTE --tags"

# version_string prints the stamp the build carries: the nearest upstream release tag plus
# our own overlay count, e.g. 1.3.17-max.13. Semver compares the numeric triple, so our
# releases order correctly between themselves and coddy -v tells the truth.
version_string() {
	local tag count
	tag=$(git describe --tags --abbrev=0 2>/dev/null) || tag=dev
	if [ "$tag" = dev ]; then
		printf 'dev\n'
		return
	fi
	count=$(git rev-list --count "$(git rev-parse "$tag^{commit}")"..HEAD 2>/dev/null) || count=0
	printf '%s-max.%s\n' "$tag" "$count"
}

# behind_and_ahead prints "<behind> <ahead>" against upstream/main.
behind_and_ahead() {
	local behind ahead
	behind=$(git rev-list --count "HEAD..$UPSTREAM_REMOTE/$UPSTREAM_BRANCH" 2>/dev/null) || behind=0
	ahead=$(git rev-list --count "$UPSTREAM_REMOTE/$UPSTREAM_BRANCH..HEAD" 2>/dev/null) || ahead=0
	printf '%s %s\n' "$behind" "$ahead"
}

# conflict_surface prints the files both sides changed since the merge base — those, and
# only those, can conflict.
conflict_surface() {
	local base
	base=$(git merge-base HEAD "$UPSTREAM_REMOTE/$UPSTREAM_BRANCH") || return 1
	comm -12 \
		<(git diff --name-only "$base"..HEAD | sort) \
		<(git diff --name-only "$base".."$UPSTREAM_REMOTE/$UPSTREAM_BRANCH" | sort)
}

cmd_check() {
	say "fetching $UPSTREAM_REMOTE ..."
	git fetch --quiet "$UPSTREAM_REMOTE" --tags || die "git fetch failed"
	read -r behind ahead < <(behind_and_ahead)
	say ""
	say "  upstream   $UPSTREAM_REMOTE/$UPSTREAM_BRANCH at $(git rev-parse --short "$UPSTREAM_REMOTE/$UPSTREAM_BRANCH")"
	say "  behind     $behind commits"
	say "  our own    $ahead commits"
	say "  latest tag $(git tag --list '1.*' --sort=-v:refname | head -1)"
	say "  our version $(version_string)"
	local surface
	surface=$(conflict_surface)
	if [ -n "$surface" ]; then
		say "  conflicts  $(printf '%s\n' "$surface" | wc -l) files both sides touch:"
		printf '%s\n' "$surface" | sed 's/^/               /'
	else
		say "  conflicts  none predicted"
	fi
	if [ "$behind" -gt 0 ]; then
		say ""
		say "upstream is ahead: run --dry to rehearse, then --apply"
		return 1
	fi
	say ""
	say "up to date with $UPSTREAM_REMOTE/$UPSTREAM_BRANCH"
	return 0
}

cmd_dry() {
	DRY_WORKTREE=$(mktemp -d)/rehearsal
	git worktree add --detach --quiet "$DRY_WORKTREE" HEAD || die "cannot create a throwaway worktree"
	trap cleanup_dry EXIT
	say "rehearsing the rebase in $DRY_WORKTREE (the working tree stays untouched)"
	if (cd "$DRY_WORKTREE" && git rebase --quiet "$UPSTREAM_REMOTE/$UPSTREAM_BRANCH" >/dev/null 2>&1); then
		say ""
		say "clean: the overlay rebases without a conflict"
		cleanup_dry
		return 0
	fi
	say ""
	say "the rebase conflicts, in:"
	(cd "$DRY_WORKTREE" && git diff --name-only --diff-filter=U) | sed 's/^/  /'
	(cd "$DRY_WORKTREE" && git rebase --abort >/dev/null 2>&1)
	cleanup_dry
	say ""
	say "resolve them in --apply; docs/MAX-OVERLAY.md says what must survive in each file"
	return 1
}

cmd_apply() {
	read -r behind _ < <(behind_and_ahead)
	if [ "$behind" -eq 0 ]; then
		say "already up to date with $UPSTREAM_REMOTE/$UPSTREAM_BRANCH"
		return 0
	fi
	if [ -n "$(git status --porcelain)" ]; then
		die "the working tree is not clean; commit or stash before rebasing"
	fi
	say "rebasing $OUR_BRANCH onto $UPSTREAM_REMOTE/$UPSTREAM_BRANCH"
	if git rebase "$UPSTREAM_REMOTE/$UPSTREAM_BRANCH"; then
		say ""
		say "rebased: $(git rev-parse --short HEAD), version $(version_string)"
		return 0
	fi
	say ""
	say "stopped on a conflict. Files:"
	git diff --name-only --diff-filter=U | sed 's/^/  /'
	say ""
	say "For each file read its row in docs/MAX-OVERLAY.md, keep upstream's structure and"
	say "re-add our hook, then: git add <file> && git rebase --continue"
	say "To back out: git rebase --abort"
	return 1
}

cmd_build() {
	local failed=0
	say "gofmt"
	local unformatted
	unformatted=$(gofmt -s -l external/gateway/max/)
	if [ -n "$unformatted" ]; then
		printf '%s\n' "$unformatted" | sed 's/^/  /'
		failed=1
	fi
	say "go vet"
	go vet -tags gateway.max ./... || failed=1
	say "build without the gateway tag (the stub must still compile)"
	go build ./... || failed=1
	say "build with the release tag set ($BUILD_TAGS)"
	make build TAGS="$BUILD_TAGS" VERSION="$(version_string)" || failed=1
	say "adapter tests"
	go test -tags gateway.max -race -count=1 "$ADAPTER_PKG" || failed=1
	say ""
	if [ "$failed" -ne 0 ]; then
		say "build failed: do not install this tree"
		return 1
	fi
	say "build ok: build/$BIN_NAME $(version_string)"
	return 0
}

# install_plan prints the deferred installer: the part that must run after the process
# replacing itself is gone. It is a separate file with plain arguments, so nothing inside it
# needs escaping and a person can read exactly what will happen on their machine.
install_plan() {
	cat <<'PLAN'
#!/usr/bin/env bash
# Written by maxgw-update.sh --install. Arguments: dst src bak stamp port start stdout_log
set -uo pipefail
dst=$1; src=$2; bak=$3; stamp=$4; port=$5; start=$6; stdout_log=$7
log="${MAXGW_LOG:-$HOME/maxgw-update.log}"
say() { printf '[%s] %s\n' "$(date '+%Y-%m-%d %H:%M:%S')" "$*" >>"$log"; }

# The service is found by reading /proc, never by pkill -f: that very pattern sits in this
# shell's own command line, so `pkill -f '<binary> serve'` killed the restarter on 12.11.2026
# and left the service down with no one to bring it back.
service_pids() {
	local p exe
	for p in /proc/[0-9]*; do
		[ -r "$p/cmdline" ] || continue
		exe=$(tr '\0' '\n' <"$p/cmdline" 2>/dev/null | head -1)
		[ "$exe" = "$dst" ] || continue
		tr '\0' '\n' <"$p/cmdline" 2>/dev/null | grep -qx 'serve' || continue
		printf '%s\n' "${p#/proc/}"
	done
}

listening() { ss -tln 2>/dev/null | grep -q ":$port "; }

say "=== install of $stamp ==="
pids=$(service_pids)
if [ -z "$pids" ]; then
	say "no running service on $dst: nothing to stop"
else
	say "stopping the service: $(echo $pids | tr '\n' ' ')"
	kill $pids 2>/dev/null
	alive=1
	for _ in $(seq 1 40); do
		alive=0
		for p in $pids; do kill -0 "$p" 2>/dev/null && alive=1; done
		[ "$alive" = 0 ] && break
		sleep 1
	done
	if [ "$alive" = 1 ]; then
		say "FAIL: the service did not stop in 40 s; nothing was replaced"
		bash "$start" >>"$log" 2>&1
		exit 1
	fi
	say "stopped"
fi

# A running binary refuses writes (ETXTBSY), so the copy is retried rather than half done.
ok=0
for _ in $(seq 1 10); do
	if cp "$src" "$dst" 2>>"$log"; then ok=1; break; fi
	sleep 1
done
if [ "$ok" = 0 ]; then
	say "FAIL: cannot write $dst; starting the previous build again"
	bash "$start" >>"$log" 2>&1
	exit 1
fi
chmod 755 "$dst"
say "installed $dst ($stamp)"

bash "$start" >>"$log" 2>&1
say "start script exit: $?"

for _ in $(seq 1 60); do listening && break; sleep 2; done
if listening; then
	say "ok: the service answers on port $port"
	"$dst" --version >>"$log" 2>&1
	exit 0
fi

say "FAIL: the service did not come up as $stamp"
tail -5 "$stdout_log" 2>/dev/null | sed 's/^/    /' >>"$log" 2>&1
if [ -n "$bak" ]; then
	say "rolling back to $bak"
	cp -p "$bak" "$dst" >>"$log" 2>&1
	chmod 755 "$dst"
	bash "$start" >>"$log" 2>&1
	sleep 10
	if listening; then
		say "rollback ok: the previous build answers again"
	else
		say "rollback FAILED: the service is still down, read the lines above"
	fi
fi
exit 1
PLAN
}

# cmd_install puts a fresh build in place without ever writing to a binary that is running.
# The order is: prove the candidate runs this configuration, back up, then hand a detached
# shell the job of stopping the service, replacing the file, starting it again and rolling
# back if the port stays silent. The restart waits 45 s so the answer that asked for the
# install reaches the surface before its writer goes away.
cmd_install() {
	local src="build/$BIN_NAME" dst="$BIN_DIR/$BIN_NAME" stamp bak port log plan
	[ -f "$src" ] || die "no $src: run --build first"
	stamp=$(version_string)
	if [ -f "$dst" ] && cmp -s "$src" "$dst"; then
		say "the installed binary is already this build ($stamp)"
		return 0
	fi
	# Nothing on disk changes until the candidate proves it can run this configuration.
	check_subsystems "$src"
	bak=""
	if [ -f "$dst" ]; then
		# The backup is named after the build it holds, not after the one replacing it: a
		# file called ...max.222.bak that actually contains max.220 is a trap at rollback time.
		local cur
		cur=$("$dst" --version 2>/dev/null | head -1 | tr ' ' '-')
		[ -n "$cur" ] || cur="$stamp"
		bak="$dst.$cur.bak-$(date +%Y%m%d-%H%M%S)"
	fi
	port="${MAXGW_PORT:-12345}"
	log="$HOME/maxgw-update.log"

	if [ "${MAXGW_INSTALL_DRY:-0}" = 1 ]; then
		say "dry run: nothing will be stopped, replaced or restarted"
		say "  build:   $src ($stamp)"
		say "  install: $dst"
		say "  backup:  ${bak:-none, nothing is installed yet}"
		say "  port:    $port, log: $log, start script: $START_SCRIPT"
		say "the plan, in order:"
		say "  1. find the service by reading /proc (never pkill -f) and stop it, waiting up to 40 s"
		say "  2. copy the build over the installed binary, retrying briefly while the file is held"
		say "  3. start the service with $START_SCRIPT"
		say "  4. wait up to 120 s for port $port, then print the version now answering"
		say "  5. if the port stays silent: restore ${bak:-the previous binary}, start it, report"
		return 0
	fi

	if [ -n "$bak" ]; then
		cp -p "$dst" "$bak" || die "cannot back up $dst"
		say "backed up the installed binary to $bak"
	fi
	plan=$(mktemp "${TMPDIR:-/tmp}/maxgw-install-XXXXXX") || die "cannot create the plan file"
	install_plan >"$plan" || die "cannot write the plan file"
	chmod 755 "$plan"
	say "restarting in 45 s; the log is $log"
	set +u
	setsid --fork bash -c "sleep 45; bash '$plan' '$dst' '$src' '$bak' '$stamp' '$port' '$START_SCRIPT' '$STDOUT_LOG'; rm -f '$plan'" >/dev/null 2>&1
	set -u
	say "install handed to a detached shell; it stops the service, replaces the binary, starts it and rolls back if the port stays silent"
	return 0
}

case "${1:-}" in
--check) cmd_check ;;
--dry) cmd_dry ;;
--apply) cmd_apply ;;
--build) cmd_build ;;
--install) cmd_install ;;
--version)
	say "$(version_string)"
	;;
*)
	sed -n '2,17p' "$0" | sed 's/^# \{0,1\}//'
	exit 2
	;;
esac
