#!/usr/bin/env bash
# Teste de repouso (produção): make up, reinicia todos os containers do projeto
# tvtl, espera REST_MIN minutos medindo CPU e confere que llm_calls não ganhou
# nenhuma linha. Não liga sessão (só lê o banco).
set -euo pipefail
cd "$(dirname "$0")/.."
MIN=${REST_MIN:-15}
TS=$(TZ=America/Sao_Paulo date +%Y%m%d-%H%M%S)
OUT=output/repouso-$TS.md
psql() { docker exec tvtl-postgres-1 psql -U tvtl -d tvtl -tAc "$1"; }

before_calls=$(psql "select count(*) from llm_calls")
before_last=$(psql "select id||' | '||created_at||' | '||purpose||' | '||model from llm_calls order by id desc limit 1")
before_sessions=$(psql "select count(*) from sessions")

make -s up >/dev/null
# só os serviços permanentes (fora os temporários de teste: gotool, postgres-test)
names=$(docker ps --filter label=com.docker.compose.project=tvtl --format '{{.Names}} {{.Label "com.docker.compose.service"}}' \
  | awk '$2 ~ /^(postgres|tvtl|api|web|tunnel|lipsync|tts-kokoro)$/ {print $1}' | sort)
docker restart $names >/dev/null
start=$(date -u +%Y-%m-%dT%H:%M:%SZ)
sleep 20 # partida

samples=$(( MIN * 60 / 30 ))
tmp=$(mktemp)
for i in $(seq 1 "$samples"); do
  docker stats --no-stream --format '{{.Name}} {{.CPUPerc}}' $names >>"$tmp"
  sleep 25
done
end=$(date -u +%Y-%m-%dT%H:%M:%SZ)

after_calls=$(psql "select count(*) from llm_calls")
after_last=$(psql "select id||' | '||created_at||' | '||purpose||' | '||model from llm_calls order by id desc limit 1")
after_sessions=$(psql "select count(*) from sessions")
active=$(psql "select count(*) from sessions where status='active'")
refused=$(psql "select count(*) from system_events where kind='no_active_session' and created_at >= '$start'")

{
  echo "# Teste de repouso — produção ($TS, horário de Brasília)"
  echo
  echo "- Janela: $start → $end (UTC), $MIN min depois de \`make up\` e de reiniciar todos os containers do projeto \`tvtl\`."
  echo "- Containers reiniciados: $(echo $names | tr ' ' ',')"
  echo "- \`llm_calls\`: $before_calls linhas antes, $after_calls depois (**$((after_calls - before_calls)) novas**)."
  echo "- Última linha de \`llm_calls\` antes: $before_last"
  echo "- Última linha de \`llm_calls\` depois: $after_last"
  echo "- \`sessions\`: $before_sessions antes, $after_sessions depois; ativas agora: $active."
  echo "- Recusas registradas (\`no_active_session\`) na janela: $refused"
  echo
  echo "| container | CPU média | CPU máxima | amostras | ≤ 2%? |"
  echo "|---|---|---|---|---|"
  awk '{gsub("%","",$2); s[$1]+=$2; n[$1]++; if ($2>m[$1]) m[$1]=$2} END {for (k in s) printf "| %s | %.2f%% | %.2f%% | %d | %s |\n", k, s[k]/n[k], m[k], n[k], (s[k]/n[k] <= 2 ? "sim" : "**não**")}' "$tmp" | sort
} >"$OUT"
rm -f "$tmp"
cat "$OUT"
if [ "$after_calls" != "$before_calls" ] || [ "$active" != "0" ] || grep -q '\*\*não\*\*' "$OUT"; then
  echo "REPOUSO REPROVADO"; exit 1
fi
echo "REPOUSO OK"
