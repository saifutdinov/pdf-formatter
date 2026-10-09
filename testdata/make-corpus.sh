#!/bin/sh
# Пересобирает тестовые PDF из того, что есть на macOS: Chrome, cupsfilter, poppler.
set -e
cd "$(dirname "$0")"
CHROME="/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
"$CHROME" --headless --disable-gpu --no-pdf-header-footer --print-to-pdf="$PWD/corpus/chrome.pdf" "file://$PWD/corpus-src/doc.html" 2>/dev/null
cupsfilter -m application/pdf corpus-src/text.txt > corpus/quartz.pdf 2>/dev/null
pdftocairo -pdf corpus/chrome.pdf corpus/cairo.pdf
# 500 страниц — не храним в репозитории, тесты пропускают, если файла нет
pdfunite $(for i in $(seq 250); do echo corpus/chrome.pdf; done) corpus/big.pdf
