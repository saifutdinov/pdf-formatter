#!/bin/sh
# Builds the standalone app (UI and fonts inside the binary) into dist/
# (dist/licensed/ with LICENSED=1):
#   PDF-Formatter.dmg          macOS app for Intel and Apple Silicon
#   pdf-formatter-windows.zip
#   pdf-formatter-linux.zip
# Only the free fonts are inside. LICENSED=1 adds fonts/licensed: such a
# build is for the holder of those licenses and must not be published.
set -e
cd "$(dirname "$0")/.."
VERSION=${VERSION:-$(date +%Y.%m.%d)}
TAGS=embed
OUT=dist
if [ "$LICENSED" = 1 ]; then
	TAGS="embed licensed"
	OUT=dist/licensed # apart from the public files
	echo "Сборка с лицензионными шрифтами ($OUT): не публиковать" >&2
fi

(cd web && npm ci --silent && npm run build --silent)
rm -rf "$OUT"
mkdir -p "$OUT"

build() { # GOOS GOARCH output [extra ldflags]
	CGO_ENABLED=0 GOOS=$1 GOARCH=$2 go build -tags "$TAGS" -trimpath -ldflags "-s -w $4" -o "$3" ./cmd/server
}

# macOS: an .app without a Dock icon (LSUIElement), stopped from the UI.
APP="$OUT/dmg/PDF Formatter.app"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
build darwin arm64 $OUT/arm64
build darwin amd64 $OUT/amd64
lipo -create -output "$APP/Contents/Resources/pdf-formatter" $OUT/arm64 $OUT/amd64
rm $OUT/arm64 $OUT/amd64
cp packaging/launch "$APP/Contents/MacOS/launch"
cp LICENSE THIRD_PARTY_LICENSES.md "$APP/Contents/Resources/"
sed "s/VERSION/$VERSION/" packaging/Info.plist > "$APP/Contents/Info.plist"
ICONSET=$OUT/icon.iconset
mkdir -p "$ICONSET"
for s in 16 32 128 256 512; do
	sips -z $s $s packaging/icon.png --out "$ICONSET/icon_${s}x${s}.png" >/dev/null
	sips -z $((s * 2)) $((s * 2)) packaging/icon.png --out "$ICONSET/icon_${s}x${s}@2x.png" >/dev/null
done
iconutil -c icns "$ICONSET" -o "$APP/Contents/Resources/icon.icns"
rm -rf "$ICONSET"
codesign --force --deep --sign - "$APP" # ad-hoc: a consistent bundle, not a Developer ID
ln -s /Applications "$OUT/dmg/Программы"
cp "packaging/Как запустить.txt" $OUT/dmg/README.txt
# Explicit size: hdiutil's own estimate is too tight for this content.
hdiutil create -quiet -volname "PDF Formatter" -srcfolder $OUT/dmg -ov -fs HFS+ -size $(( $(du -sm $OUT/dmg | cut -f1) + 40 ))m -format UDZO $OUT/PDF-Formatter.dmg
rm -rf $OUT/dmg
echo "$OUT/PDF-Formatter.dmg  $(du -h $OUT/PDF-Formatter.dmg | cut -f1)"

pack() { # folder name
	cp "packaging/Как запустить.txt" "$OUT/$1/README.txt" # ASCII name: Windows unzip mangles UTF-8 names
	cp LICENSE THIRD_PARTY_LICENSES.md "$OUT/$1/"
	(cd "$OUT" && zip -qr "$1.zip" "$1" && rm -rf "$1")
	echo "$OUT/$1.zip  $(du -h "$OUT/$1.zip" | cut -f1)"
}

# Windows: no console window; double click starts it or opens the running one.
mkdir $OUT/pdf-formatter-windows
build windows amd64 $OUT/pdf-formatter-windows/pdf-formatter.exe "-H windowsgui"
pack pdf-formatter-windows

mkdir $OUT/pdf-formatter-linux
build linux amd64 $OUT/pdf-formatter-linux/pdf-formatter
pack pdf-formatter-linux
