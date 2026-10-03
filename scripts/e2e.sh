#!/bin/sh
# End-to-end check with two real Claude Code sessions racing for one file.
# Session A edits calc.go, then sleeps while still holding it. Session B starts
# while A holds the file, is told to wait, and should be woken with A's changes.
# Costs a few cents (Haiku by default). Set KEEP=1 to keep the work directory.
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
model=${MODEL:-haiku}
work=$(mktemp -d "${TMPDIR:-/tmp}/file-baton-e2e.XXXXXX")
if [ "${KEEP:-}" != 1 ]; then trap 'rm -rf "$work"' EXIT; fi
echo "work dir: $work"

cd "$work"
git init -q -b main
git config user.email e2e@example.com
git config user.name e2e
cat > calc.go <<'EOF'
package calc

func Add(a, b int) int { return a + b }
EOF
cat > slow-build.sh <<'EOF'
#!/bin/sh
# Stands in for a long build so session A keeps calc.go for a while.
sleep 30
echo build ok
EOF
chmod +x slow-build.sh
git add -A
git commit -qm init

# The prompt goes on stdin: --allowedTools takes several values and would swallow it.
run_session() {
  printf '%s' "$1" | claude -p --plugin-dir "$root" --model "$model" \
    --permission-mode acceptEdits --allowedTools "Bash(./slow-build.sh)"
}

log="$work/.git/file-baton/log"

run_session "In calc.go, add a function Sub(a, b int) int that returns a - b. After that edit, run ./slow-build.sh in the foreground (not in the background) and wait for it to finish. After it finishes, add the comment '// Sub subtracts b from a.' on the line above Sub." > a.out 2>&1 &
pa=$!
# Start B only once A holds calc.go.
i=0
until grep -q 'pre-edit allow calc.go' "$log" 2>/dev/null; do
  i=$((i + 1))
  if [ "$i" -gt 120 ]; then echo "A never edited calc.go"; break; fi
  sleep 0.5
done
sleep 2
run_session "In calc.go, add a function Mul(a, b int) int that returns a * b." > b.out 2>&1 &
pb=$!
wait "$pa" || true
wait "$pb" || true

echo "--- calc.go"
cat calc.go
echo "--- session A"
cat a.out
echo "--- session B"
cat b.out
echo "--- file-baton log"
cat "$log"

fail=0
check() {
  if eval "$2"; then echo "ok:   $1"; else echo "FAIL: $1"; fail=1; fi
}
check "Sub was added"            "grep -q 'func Sub' calc.go"
check "Mul was added"            "grep -q 'func Mul' calc.go"
check "Sub comment was added"    "grep -q '// Sub subtracts' calc.go"
check "B was blocked while A held calc.go" "grep -q 'pre-edit deny calc.go' '$log'"
check "B received the file (wake-up or handoff)" "grep -Eq 'delivered handoff|pre-edit allow calc.go' '$log'"
exit "$fail"
