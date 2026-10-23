#!/bin/sh
# Installs Pracre for the current user (no root needed). Run from the unpacked folder.
set -e
here=$(cd "$(dirname "$0")" && pwd)
mkdir -p "$HOME/.local/bin" "$HOME/.local/share/applications" "$HOME/.local/share/icons/hicolor/512x512/apps" "$HOME/.local/share/mime/packages"
install -m 755 "$here/pracre" "$HOME/.local/bin/pracre"
install -m 644 "$here/pracre.png" "$HOME/.local/share/icons/hicolor/512x512/apps/pracre.png"
sed "s|^Exec=pracre|Exec=$HOME/.local/bin/pracre|" "$here/pracre.desktop" > "$HOME/.local/share/applications/pracre.desktop"
install -m 644 "$here/pracre-mime.xml" "$HOME/.local/share/mime/packages/pracre.xml"
update-mime-database "$HOME/.local/share/mime" 2>/dev/null || true
update-desktop-database "$HOME/.local/share/applications" 2>/dev/null || true
echo "Pracre is installed. Start it from your applications menu or run: pracre"
