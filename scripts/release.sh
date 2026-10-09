#!/bin/sh
# Builds the standalone app (UI and fonts inside the binary) into dist/:
#   PDF-Formatter.dmg          macOS app for Intel and Apple Silicon
#   pdf-formatter-windows.zip
#   pdf-formatter-linux.zip
# Only the free fonts are inside. LICENSED=1 adds fonts/licensed: such a
# build is for the holder of those licenses and must not be published.
set -e
cd "$(dirname "$0")/.."
VERSION=$(date +%Y.%m.%d)
TAGS=embed
if [ "$LICENSED" = 1 ]; then
	TAGS="embed licensed"
	echo "Сборка с лицензионными шрифтами: не публиковать" >&2
fi

(cd web && npm ci --silent && npm run build --silent)
rm -rf dist
mkdir -p dist

build() { # GOOS GOARCH output [extra ldflags]
	CGO_ENABLED=0 GOOS=$1 GOARCH=$2 go build -tags "$TAGS" -trimpath -ldflags "-s -w $4" -o "$3" ./cmd/server
}

# macOS: an .app without a Dock icon (LSUIElement), stopped from the UI.
APP="dist/dmg/PDF Formatter.app"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
build darwin arm64 dist/arm64
build darwin amd64 dist/amd64
lipo -create -output "$APP/Contents/Resources/pdf-formatter" dist/arm64 dist/amd64
rm dist/arm64 dist/amd64
cp packaging/launch "$APP/Contents/MacOS/launch"
cp LICENSE THIRD_PARTY_LICENSES.md "$APP/Contents/Resources/"
sed "s/VERSION/$VERSION/" packaging/Info.plist > "$APP/Contents/Info.plist"
ICONSET=dist/icon.iconset
mkdir -p "$ICONSET"
for s in 16 32 128 256 512; do
	sips -z $s $s packaging/icon.png --out "$ICONSET/icon_${s}x${s}.png" >/dev/null
	sips -z $((s * 2)) $((s * 2)) packaging/icon.png --out "$ICONSET/icon_${s}x${s}@2x.png" >/dev/null
done
iconutil -c icns "$ICONSET" -o "$APP/Contents/Resources/icon.icns"
rm -rf "$ICONSET"
codesign --force --deep --sign - "$APP" # ad-hoc: a consistent bundle, not a Developer ID
ln -s /Applications "dist/dmg/Программы"
cp "packaging/Как запустить.txt" dist/dmg/README.txt
# Explicit size: hdiutil's own estimate is too tight for this content.
hdiutil create -quiet -volname "PDF Formatter" -srcfolder dist/dmg -ov -fs HFS+ -size $(( $(du -sm dist/dmg | cut -f1) + 40 ))m -format UDZO dist/PDF-Formatter.dmg
rm -rf dist/dmg
echo "dist/PDF-Formatter.dmg  $(du -h dist/PDF-Formatter.dmg | cut -f1)"

pack() { # folder name
	cp "packaging/Как запустить.txt" "dist/$1/README.txt" # ASCII name: Windows unzip mangles UTF-8 names
	cp LICENSE THIRD_PARTY_LICENSES.md "dist/$1/"
	(cd dist && zip -qr "$1.zip" "$1" && rm -rf "$1")
	echo "dist/$1.zip  $(du -h "dist/$1.zip" | cut -f1)"
}

# Windows: no console window; double click starts it or opens the running one.
mkdir dist/pdf-formatter-windows
build windows amd64 dist/pdf-formatter-windows/pdf-formatter.exe "-H windowsgui"
pack pdf-formatter-windows

mkdir dist/pdf-formatter-linux
build linux amd64 dist/pdf-formatter-linux/pdf-formatter
pack pdf-formatter-linux
