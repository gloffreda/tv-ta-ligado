#!/usr/bin/env bash
# Troca a senha do site. Grava no .env SÓ o hash (argon2id) em
# SITE_PASSWORD_HASH, cria SITE_SESSION_SECRET (e o da homologação) se
# faltarem, recria a api e mostra a senha UMA vez, no final.
#   make set-password            gera uma senha forte
#   make set-password ASK=1      você digita a sua (mínimo 12 caracteres)
# Trocar a senha desloga todo mundo (o cookie é assinado com o hash).
set -euo pipefail
cd "$(dirname "$0")/.."
touch .env
chmod 600 .env

if [ "${ASK:-}" = "1" ]; then
  read -r -s -p "Nova senha: " pw; echo
  read -r -s -p "Repita: " pw2; echo
  [ "$pw" = "$pw2" ] || { echo "as senhas não conferem"; exit 1; }
else
  # 24 caracteres sem ambíguos (0/O, 1/l/I): ~140 bits
  pw=$(set +o pipefail; LC_ALL=C tr -dc 'ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnpqrstuvwxyz23456789' </dev/urandom | head -c 24)
fi

hash=$(printf '%s' "$pw" | docker compose -p tvtl run --rm --no-deps -T tvtl hash-password 2>/dev/null | tail -1)
case "$hash" in argon2id.*) ;; *) echo "falha ao gerar o hash"; exit 1 ;; esac

setvar() { # setvar NOME VALOR (substitui ou acrescenta, sem mexer no resto)
  local tmp; tmp=$(mktemp)
  grep -v "^$1=" .env >"$tmp" || true
  printf '%s=%s\n' "$1" "$2" >>"$tmp"
  cat "$tmp" >.env && rm -f "$tmp"
}
gen() { head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n'; }

setvar SITE_PASSWORD_HASH "$hash"
grep -qE '^SITE_SESSION_SECRET=.+' .env || setvar SITE_SESSION_SECRET "$(gen)"
grep -qE '^SITE_STAGING_SESSION_SECRET=.+' .env || setvar SITE_STAGING_SESSION_SECRET "$(gen)"

# a api lê o .env na partida: recria só ela (produção e, se estiver de pé, homologação)
if docker ps --format '{{.Names}}' | grep -qx tvtl-api-1; then
  docker compose -p tvtl up -d --force-recreate --no-deps api >/dev/null 2>&1
fi
if docker ps --format '{{.Names}}' | grep -qx tvtl-staging-api-1; then
  docker compose -p tvtl-staging -f compose.staging.yaml up -d --force-recreate --no-deps api >/dev/null 2>&1
fi
mkdir -p .make && touch .make/env.stamp

echo
echo "Senha do site gravada (no .env só o hash). Anote agora; ela não é mostrada de novo:"
echo
echo "    $pw"
echo
