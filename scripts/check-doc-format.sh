#!/bin/sh

set -eu

failed=0
files=$(find README.md docs samples -type f -name '*.md' -print | sort)

if grep -nH '[[:blank:]]$' $files; then
	echo 'documentation contains trailing whitespace' >&2
	failed=1
fi

for file in $files; do
	if ! awk '
		BEGIN { fence = 0; h1 = 0; previous = 0; failed = 0 }
		/^```/ {
			if (!fence && $0 == "```") {
				printf "%s:%d: opening code fence must declare a language\n", FILENAME, FNR
				failed = 1
			}
			fence = !fence
			next
		}
		fence { next }
		/^#+ / {
			heading = $0
			sub(/ .*/, "", heading)
			level = length(heading)
			if (level == 1) h1++
			if (previous > 0 && level > previous + 1) {
				printf "%s:%d: heading level skips from H%d to H%d\n", FILENAME, FNR, previous, level
				failed = 1
			}
			previous = level
		}
		END {
			if (fence) {
				printf "%s: unclosed code fence\n", FILENAME
				failed = 1
			}
			if (h1 != 1) {
				printf "%s: expected exactly one H1, found %d\n", FILENAME, h1
				failed = 1
			}
			exit failed
		}
	' "$file"; then
		failed=1
	fi
done

perl -ne '
	while (/\[[^]]*\]\(([^)]+)\)/g) {
		$target = $1;
		next if $target =~ m{^(?:https?://|mailto:|#)};
		$target =~ s/#.*$//;
		print "$ARGV\t$target\n" if length $target;
	}
' $files | while IFS="$(printf '\t')" read -r source target; do
	base=$(dirname "$source")
	if [ ! -e "$base/$target" ]; then
		echo "$source: broken relative link: $target" >&2
		exit 1
	fi
done || failed=1

if [ "$failed" -ne 0 ]; then
	exit 1
fi

echo 'documentation format and link checks passed'
