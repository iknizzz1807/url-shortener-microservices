#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

docker run --rm --entrypoint /bin/bash \
  -v "${SCRIPT_DIR}:/work" \
  -w /work \
  ubuntu:22.04 \
  -lc 'apt-get update >/dev/null && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends texlive-latex-base texlive-latex-recommended texlive-latex-extra texlive-lang-other texlive-fonts-recommended >/dev/null && pdflatex -interaction=nonstopmode -halt-on-error main.tex && pdflatex -interaction=nonstopmode -halt-on-error main.tex && chmod -R a+rwX /work'
