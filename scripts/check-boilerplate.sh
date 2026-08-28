#!/usr/bin/env bash
#
# The nine modules copy Logger, nopLogger, Option, WithLogger and WithName
# verbatim so they stay diffable (AGENTS.md, "Boilerplate stays identical";
# ADR-0002 records why the duplication is deliberate). Prose cannot enforce
# that — a change to one copy drifts silently. This does.
#
# Each block below is extracted from every module's <module>.go by its first
# and last line and compared against the reference module. WithName's doc
# comment is deliberately module-specific, so only its signature and body are
# compared.
#
# Usage: scripts/check-boilerplate.sh [reference-module]   (default: redis)

set -uo pipefail

cd "$(dirname "$0")/.."

REFERENCE=${1:-redis}
MODULES=$(find . -name go.mod -not -path './.git/*' | xargs -I{} dirname {} | sed 's|^\./||' | sort)

# name:first-line:last-line — the extraction ranges, one per copied block.
BLOCKS=(
	'Logger:// Logger is satisfied by:func (nopLogger) Error(string, ...any) {}'
	'Option/WithLogger:// Option configures a [Component].:	return func(c *Component) { c.log = l }'
	'WithName:func WithName(name string) Option {:	return func(c *Component) { c.name = name }'
)

extract() { # file first last
	awk -v first="$2" -v last="$3" '
		index($0, first) == 1 { inside = 1 }
		inside { print }
		inside && index($0, last) == 1 { exit }
	' "$1"
}

fail=0
for block in "${BLOCKS[@]}"; do
	name=${block%%:*}
	rest=${block#*:}
	first=${rest%:*}
	last=${rest##*:}

	reference_text=$(extract "$REFERENCE/$REFERENCE.go" "$first" "$last")
	if [ -z "$reference_text" ]; then
		echo "▶ boilerplate: cannot extract '$name' from the reference module $REFERENCE"
		fail=1
		continue
	fi

	for mod in $MODULES; do
		[ "$mod" = "$REFERENCE" ] && continue
		text=$(extract "$mod/$mod.go" "$first" "$last")
		if [ -z "$text" ]; then
			echo "▶ boilerplate: $mod is missing the '$name' block"
			fail=1
			continue
		fi
		if [ "$text" != "$reference_text" ]; then
			echo "▶ boilerplate: $mod/$mod.go '$name' differs from $REFERENCE:"
			diff <(echo "$reference_text") <(echo "$text") | sed 's/^/  /'
			fail=1
		fi
	done
done

if [ $fail -ne 0 ]; then
	echo "the nine copies must stay identical — change one and change all nine the same way"
	exit 1
fi
echo "▶ boilerplate: nine copies identical"
