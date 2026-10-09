#!/bin/sh
# Encrypted test files (qpdf from the dev image): every revision of the
# standard security handler, user password "user", owner password "owner".
#   docker build --target dev -t pdf-formatter-dev . && \
#   docker run --rm -v "$PWD/testdata:/t" -w /t pdf-formatter-dev sh make-encrypted.sh
set -e
src=corpus/quartz.pdf
mkdir -p encrypted
qpdf --allow-weak-crypto --encrypt user owner 40 -- $src encrypted/rc4-40.pdf
qpdf --allow-weak-crypto --encrypt user owner 128 --use-aes=n -- $src encrypted/rc4-128.pdf
qpdf --encrypt user owner 128 --use-aes=y -- $src encrypted/aes-128.pdf
qpdf --encrypt user owner 256 -- $src encrypted/aes-256.pdf
qpdf --allow-weak-crypto --encrypt user owner 256 --force-R5 -- $src encrypted/aes-256-r5.pdf
qpdf --encrypt "пароль" "владелец" 256 -- $src encrypted/aes-256-utf8.pdf
# No user password, changes forbidden: opens, but editing needs the owner password.
qpdf --encrypt "" owner 256 --modify=none -- $src encrypted/owner-only.pdf
qpdf --allow-weak-crypto --encrypt "" owner 128 --use-aes=n --modify=none -- $src encrypted/owner-only-rc4.pdf
