#!/bin/bash

set -e  # Stoppe bei Fehlern

VALIDATOR_COUNT=4
SECRETS_BASE=~/xgr-secrets
REMOTE_TARGET_DIR=~/xgrchain/data
GENESIS_VALIDATORS_DIR=./validators  # Für Genesis-Tool lokal auf dem Mainnode

# SSH-Ziele (Hosts der Validator-Nodes)
HOSTS=(
  "xgradmin@217.154.225.157"  # Mainnode
  "xgradmin@217.154.225.155"
  "xgradmin@85.215.128.146"
  "xgradmin@217.154.237.188"
)

echo "🔐 Erzeuge oder verwende bestehende Validator-Secrets in $SECRETS_BASE …"
mkdir -p "$SECRETS_BASE"

# Robust: Lösche Genesis-Validatoren-Verzeichnis NUR wenn es existiert und KEIN Sourcecode drin ist
echo "🔎 Prüfe $GENESIS_VALIDATORS_DIR auf Sourcecode-Schutz..."
if [[ -d "$GENESIS_VALIDATORS_DIR" ]]; then
  if [[ -f "$GENESIS_VALIDATORS_DIR/go.mod" ]]; then
    echo "⚠️  Ordner $GENESIS_VALIDATORS_DIR existiert und enthält Sourcecode – Abbruch!"
    exit 1
  else
    rm -rf "$GENESIS_VALIDATORS_DIR"
  fi
fi
mkdir -p "$GENESIS_VALIDATORS_DIR"

for i in $(seq 1 $VALIDATOR_COUNT); do
  LOCAL_DIR="${SECRETS_BASE}/validator-${i}"
  CONS_DIR="${LOCAL_DIR}/consensus"
  TARGET_HOST="${HOSTS[$((i-1))]}"

  echo ""
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
  echo "🛠️  Validator $i → Host: $TARGET_HOST"
  echo "📁 Lokaler Key-Pfad: $LOCAL_DIR"
  echo "📦 Ziel auf Node: $REMOTE_TARGET_DIR"
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

  # Keys nur erzeugen, wenn sie nicht existieren
  if [[ -f "$CONS_DIR/validator.key" && -f "$CONS_DIR/validator-bls.key" ]]; then
    echo "ℹ️  Keys für Validator $i bereits vorhanden – überspringe Erzeugung"
  else
    echo "🔑 Erzeuge Keys für Validator $i"
    mkdir -p "$LOCAL_DIR"
    xgrchain secrets init \
      --data-dir "$LOCAL_DIR" \
      --insecure \
      --bls \
      --ecdsa \
      --network \
      > /dev/null
  fi

  # Genesis-kompatible Kopie
  echo "📂 Baue Genesis-Ordner ./validators/validator-${i}/consensus"
  GEN_CONS_DIR="$GENESIS_VALIDATORS_DIR/validator-${i}/consensus"
  mkdir -p "$GEN_CONS_DIR"
  cp "$CONS_DIR/validator.key" "$GEN_CONS_DIR/"
  cp "$CONS_DIR/validator-bls.key" "$GEN_CONS_DIR/"

  # Verteilung an Nodes
  if [[ "$TARGET_HOST" == "xgradmin@217.154.225.157" ]]; then
    echo "🔁 Mainnode – lokale Kopie"
    rm -rf "$REMOTE_TARGET_DIR"
    mkdir -p "$REMOTE_TARGET_DIR"
    cp -r "$LOCAL_DIR"/* "$REMOTE_TARGET_DIR/"
  else
    echo "📤 Remote vorbereiten auf $TARGET_HOST"
    ssh "$TARGET_HOST" "rm -rf $REMOTE_TARGET_DIR && mkdir -p $REMOTE_TARGET_DIR"
    scp -rp "$LOCAL_DIR"/* "$TARGET_HOST:$REMOTE_TARGET_DIR/"
  fi

  echo "✅ Validator $i bereit"
done

echo ""
echo "🎉 Alle Secrets verteilt"
echo "📁 Genesis-Struktur erstellt unter: $GENESIS_VALIDATORS_DIR"
