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
#   --install   back up the installed binary, install the fresh build, restart detached
#   --version   the version string the next build carries
#
# --check and --dry are safe unattended. --apply, --build and --install change the tree or
# the machine, so they are for a person (or an agent acting on that person's word).
set -uo pipefail

UPSTREAM_REMOTE="${MAXGW_UPSTREAM_REMOTE:-upstream}"
UPSTREAM_BRANCH="${MAXGW_UPSTREAM_BRANCH:-main}"
OUR_BRANCH="${MAXGW_BRANCH:-main}"
BUILD_TAGS="${MAXGW_TAGS:-http,ui,scheduler,memory,cli,gateway,swarm}"
ADAPTER_PKG="./external/gateway/max/"
BIN_DIR="${MAXGW_BIN_DIR:-$HOME/.local/bin}"
START_SCRIPT="${MAXGW_START_SCRIPT:-$HOME/start_coddy.sh}"
BIN_NAME=coddy

say() { printf '%s\n' "$*"; }
die() { printf 'error: %s\n' "$*" >&2; exit 2; }

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

cmd_install() {
	local src="build/$BIN_NAME" dst="$BIN_DIR/$BIN_NAME" stamp bak
	[ -f "$src" ] || die "no $src: run --build first"
	stamp=$(version_string)
	if [ -f "$dst" ] && cmp -s "$src" "$dst"; then
		say "the installed binary is already this build ($stamp)"
		return 0
	fi
	if [ -f "$dst" ]; then
		bak="$dst.$stamp.bak-$(date +%Y%m%d-%H%M%S)"
		cp -p "$dst" "$bak" || die "cannot back up $dst"
		say "backed up the installed binary to $bak"
	fi
	install -m 0755 "$src" "$dst" || die "cannot install $dst"
	say "installed $dst ($stamp)"
	# Coddy runs this script from inside the very process it is replacing, so the restart
	# cannot happen here: a detached shell waits for this turn to end, stops the service,
	# starts it again and writes what happened. The wait is long enough for the answer that
	# asked for the install to reach the surface before its writer goes away.
	local log="$HOME/maxgw-update.log"
	say "restarting in 45 s; the log is $log"
	setsid --fork bash -c "
		sleep 45
		echo \"=== \$(date -Is) restart after install of $stamp ===\" >>'$log'
		pkill -x $BIN_NAME
		sleep 2
		bash '$START_SCRIPT' >>'$log' 2>&1
		sleep 3
		'$dst' -v >>'$log' 2>&1
		pgrep -ax $BIN_NAME >>'$log' 2>&1
	" >/dev/null 2>&1
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
