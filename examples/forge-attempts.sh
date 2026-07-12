#!/usr/bin/env bash
# Demonstrates the scoring channel's security property: it *verifies*
# outcomes but does not *prevent* forgery — a client can bypass the lesson
# UI and POST a correct submission (quiz hash / exercise screenshot) directly.
# Run against a deployed platform: BASE=http://localhost:8080 ./forge-attempts.sh site/challenges.json proof.txt
set -euo pipefail
BASE="${BASE:-http://localhost:8080}"
CHALLENGES="${1:-site/challenges.json}"
PROOF="${2:-}"   # a data:image/... URL of a valid exercise screenshot

qh=$(python3 -c "import json;print([c for c in json.load(open('$CHALLENGES')) if c['name'].startswith('quiz')][0]['hash'])")
qf=$(python3 -c "import json;print([c for c in json.load(open('$CHALLENGES')) if c['name'].startswith('quiz')][0]['flags'][0])")
eh=$(python3 -c "import json;print([c for c in json.load(open('$CHALLENGES')) if c['name'].startswith('exercise')][0]['hash'])")

echo "# forged quiz (correct hash, no UI):"
curl -s -X POST "$BASE/api/v1/challenges/attempt" -H 'Content-Type: application/json' -d "{\"challenge_hash\":\"$qh\",\"submission\":\"$qf\"}"; echo
if [ -n "$PROOF" ]; then
  echo "# forged exercise (screenshot proof, phash grading):"
  python3 - "$eh" "$PROOF" "$BASE" <<'PY'
import sys,json,urllib.request
h,proof,base=sys.argv[1],sys.argv[2],sys.argv[3]
r=urllib.request.Request(base+"/api/v1/challenges/attempt",
  data=json.dumps({"challenge_hash":h,"submission":open(proof).read()}).encode(),
  headers={"Content-Type":"application/json"})
print(urllib.request.urlopen(r).read().decode())
PY
fi
echo "# forged wrong answer (rejected):"
curl -s -X POST "$BASE/api/v1/challenges/attempt" -H 'Content-Type: application/json' -d "{\"challenge_hash\":\"$qh\",\"submission\":\"deadbeef\"}"; echo
echo "# scoreboard:"
curl -s "$BASE/api/v1/scoreboard"; echo
