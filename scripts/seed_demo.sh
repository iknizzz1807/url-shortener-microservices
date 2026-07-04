#!/bin/bash
set -euo pipefail

BASE_URL="${BASE_URL:-http://localhost:8080}"
DEMO_EMAIL="demo@email.com"
DEMO_PASS="demopassword"

UA_CHROME="Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 Chrome/120.0.0.0 Safari/537.36"
UA_FIREFOX="Mozilla/5.0 (Windows NT 10.0; rv:121.0) Gecko/20100101 Firefox/121.0"
UA_SAFARI="Mozilla/5.0 (Macintosh; Intel Mac OS X 14_2) AppleWebKit/605.1.15 Safari/604.1"
UA_MOBILE="Mozilla/5.0 (iPhone; CPU iPhone OS 17_1 like Mac OS X) AppleWebKit/605.1.15 Mobile/15E148"
UAS=("$UA_CHROME" "$UA_FIREFOX" "$UA_SAFARI" "$UA_MOBILE")

REF_GOOGLE="https://www.google.com/search?q=url+shortener"
REF_TWITTER="https://twitter.com/share"
REF_REDDIT="https://reddit.com/r/programming"
REF_GITHUB="https://github.com/trending"
REF_FACEBOOK="https://facebook.com"
REFS=("$REF_GOOGLE" "$REF_TWITTER" "$REF_REDDIT" "$REF_GITHUB" "$REF_FACEBOOK" "")

URLS_DATA=(
  "How to Learn Go in 2026|https://go.dev/doc/"
  "Awesome Kubernetes Resources|https://kubernetes.io/docs/home/"
  "Microservices Patterns Guide|https://microservices.io/patterns/index.html"
  "Rust vs Go Performance|https://benchmarksgame-team.pages.debian.net/benchmarksgame/"
  "System Design Interview Prep|https://github.com/donnemartin/system-design-primer"
  "Docker Compose Deep Dive|https://docs.docker.com/compose/"
)

echo "============================================"
echo "  Seeding Demo Data"
echo "============================================"

echo ""
echo "1. Registering demo user..."
REG=$(curl -sf -X POST "${BASE_URL}/api/auth/register" \
  -H "Content-Type: application/json" \
  -d "{\"email\":\"${DEMO_EMAIL}\",\"password\":\"${DEMO_PASS}\"}" 2>/dev/null || true)

if echo "$REG" | grep -q '"user_id"'; then
  USER_ID=$(echo "$REG" | grep -oP '"user_id":"\K[^"]+')
  echo "   Registered: ${USER_ID}"
else
  echo "   User exists, logging in..."
fi

TOKEN=$(curl -sf -X POST "${BASE_URL}/api/auth/login" \
  -H "Content-Type: application/json" \
  -d "{\"email\":\"${DEMO_EMAIL}\",\"password\":\"${DEMO_PASS}\"}" | grep -oP '"token":"\K[^"]+')
echo "   Token acquired"

echo ""
echo "2. Creating short URLs..."
CODES=()
for entry in "${URLS_DATA[@]}"; do
  TITLE="${entry%%|*}"
  URL="${entry##*|}"
  RES=$(curl -sf -X POST "${BASE_URL}/api/shorten" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer ${TOKEN}" \
    -d "{\"url\":\"${URL}\",\"expires_in_hours\":8760}" 2>/dev/null || true)
  CODE=$(echo "$RES" | grep -oP '"short_code":"\K[^"]+')
  if [ -n "$CODE" ]; then
    CODES+=("$CODE")
    echo "   [${#CODES[@]}] ${CODE} -> ${TITLE}"
  fi
done

echo ""
echo "3. Getting user_id from DB..."
USER_ID=$(docker exec url-shortener-microservices-user_db-1 psql -U useruser -d userdb -tAc "SELECT id FROM users WHERE email='${DEMO_EMAIL}' LIMIT 1" 2>/dev/null || true)
echo "   user_id=${USER_ID}"

echo ""
echo "4. Setting URL display titles..."
IDX=0
for code in "${CODES[@]}"; do
  TITLE="${URLS_DATA[$IDX]}"
  TITLE="${TITLE%%|*}"
  docker exec url-shortener-microservices-url_db-1 psql -U urluser -d urldb \
    -c "UPDATE urls SET user_email='${TITLE}' WHERE short_code='${code}'" >/dev/null 2>&1
  IDX=$((IDX + 1))
done
echo "   Done"

echo ""
echo "5. Clearing old analytics + notifications..."
docker exec url-shortener-microservices-analytics_db-1 psql -U analyticsuser -d analyticsdb \
  -c "DELETE FROM clicks WHERE short_code IN ($(printf "'%s'," "${CODES[@]}" | sed 's/,$//'));" >/dev/null 2>&1
docker exec url-shortener-microservices-analytics_db-1 psql -U analyticsuser -d analyticsdb \
  -c "DELETE FROM milestones WHERE short_code IN ($(printf "'%s'," "${CODES[@]}" | sed 's/,$//'));" >/dev/null 2>&1
docker exec url-shortener-microservices-notification_db-1 psql -U notificationuser -d notificationdb \
  -c "DELETE FROM notifications WHERE user_id='${USER_ID}';" >/dev/null 2>&1
echo "   Done"

echo ""
echo "6. Generating clicks (~150 per code, 30 days)..."
{
  for code in "${CODES[@]}"; do
    for days_ago in $(seq 0 29); do
      base=$((RANDOM % 6 + 2))
      if [ "$days_ago" -lt 3 ]; then base=$((base + RANDOM % 5)); fi
      if [ "$days_ago" -lt 7 ]; then base=$((base + RANDOM % 3)); fi

      for ((c=0; c<base; c++)); do
        hour=$((RANDOM % 24))
        minute=$((RANDOM % 60))
        sec=$((RANDOM % 60))
        ts=$(date -u -d "-${days_ago} days +${hour} hours +${minute} minutes +${sec} seconds" "+%Y-%m-%d %H:%M:%S+00" 2>/dev/null || \
             date -u -v-${days_ago}d -v+${hour}H -v+${minute}M -v+${sec}S "+%Y-%m-%d %H:%M:%S+00" 2>/dev/null)
        [ -z "$ts" ] && ts="2026-06-20 12:00:00+00"

        UA="${UAS[$((RANDOM % 4))]}"
        REF="${REFS[$((RANDOM % 6))]}"
        IP_HASH=$(echo -n "192.168.$((RANDOM % 256)).$((RANDOM % 256))" | sha256sum | cut -d' ' -f1)

        printf "%s\t%s\t%s\t%s\t%s\n" "$code" "$ts" "$IP_HASH" "$UA" "$REF"
      done
    done
  done
} | docker exec -i url-shortener-microservices-analytics_db-1 psql -U analyticsuser -d analyticsdb \
  -c "COPY clicks (short_code, clicked_at, ip_hash, user_agent, referer) FROM STDIN;" >/dev/null 2>&1

echo "   Inserted clicks"
for code in "${CODES[@]}"; do
  count=$(docker exec url-shortener-microservices-analytics_db-1 psql -U analyticsuser -d analyticsdb -tAc "SELECT count(*) FROM clicks WHERE short_code='${code}'" 2>/dev/null)
  echo "   ${code}: ${count} clicks"
done

echo ""
echo "7. Inserting milestones..."
MILESTONE_SQL=""
for code in "${CODES[@]}"; do
  for m in 10 100 1000; do
    triggered=$(date -u -d "-$((RANDOM % 10 + 1)) days" "+%Y-%m-%d %H:%M:%S+00" 2>/dev/null || echo "2026-06-25 12:00:00+00")
    MILESTONE_SQL+="INSERT INTO milestones (short_code, milestone, triggered_at) VALUES ('${code}', ${m}, '${triggered}') ON CONFLICT (short_code, milestone) DO NOTHING;"
  done
done
docker exec -i url-shortener-microservices-analytics_db-1 psql -U analyticsuser -d analyticsdb -c "$MILESTONE_SQL" >/dev/null 2>&1
echo "   Done"

echo ""
echo "8. Generating notifications..."
NOTIF_SQL=""
for code in "${CODES[@]}"; do
  nts=$(date -u -d "-$((RANDOM % 5 + 1)) days" "+%Y-%m-%d %H:%M:%S+00" 2>/dev/null || echo "2026-06-28 12:00:00+00")
  NOTIF_SQL+="INSERT INTO notifications (user_id, event_type, payload, status, created_at, sent_at) VALUES ('${USER_ID}', 'url.created', '{\"short_code\":\"${code}\",\"event_type\":\"url.created\",\"user_id\":\"${USER_ID}\",\"user_email\":\"${DEMO_EMAIL}\"}', 'sent', '${nts}', '${nts}');"
done
for code in "${CODES[@]:0:3}"; do
  mts=$(date -u -d "-$((RANDOM % 2 + 1)) days" "+%Y-%m-%d %H:%M:%S+00" 2>/dev/null || echo "2026-07-02 12:00:00+00")
  NOTIF_SQL+="INSERT INTO notifications (user_id, event_type, payload, status, created_at, sent_at) VALUES ('${USER_ID}', 'milestone.reached', '{\"short_code\":\"${code}\",\"milestone\":100,\"total_clicks\":100,\"event_type\":\"milestone.reached\",\"user_id\":\"${USER_ID}\",\"user_email\":\"${DEMO_EMAIL}\"}', 'sent', '${mts}', '${mts}');"
done
docker exec -i url-shortener-microservices-notification_db-1 psql -U notificationuser -d notificationdb -c "$NOTIF_SQL" >/dev/null 2>&1
echo "   Done"

echo ""
echo "============================================"
echo "  Seed complete!"
echo "  Login: ${DEMO_EMAIL} / ${DEMO_PASS}"
echo "  Dashboard: http://localhost:8080"
echo "============================================"
