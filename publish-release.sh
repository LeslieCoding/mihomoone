#!/bin/bash
# MihomoOne 发布脚本：构建 APK → 生成 latest.json → 上传 GitHub Release
# 用法: bash publish-release.sh            # 构建并发布
#       bash publish-release.sh --skip-build /path/to.apk   # 只发布
set -euo pipefail
cd "$(dirname "$0")"

VERSION_NAME=$(grep -oP 'versionName = "\K[0-9.]+' build.gradle.kts | head -1)
VERSION_CODE=$(grep -oP 'versionCode = \K[0-9]+' build.gradle.kts | head -1)
TAG="v$VERSION_NAME"
APK="app/build/intermediates/apk/alpha/release/mihomoone-$VERSION_NAME-alpha-arm64-v8a-release.apk"
REPO="LeslieCoding/mihomoone"

if [ "${1:-}" = "--skip-build" ]; then
  APK="$2"
else
  export JAVA_HOME="D:\\Software\\jdk-21" ANDROID_HOME="D:\\Software\\Android-SDK"
  export ANDROID_NDK_HOME="$ANDROID_HOME\\ndk\\29.0.14206865" GOROOT="D:\\Software\\go1.26"
  export PATH="/d/Software/go1.26/bin:/d/Software/jdk-21/bin:$PATH"
  GRADLE="$HOME/.gradle/wrapper/dists/gradle-8.11.1-all/6gcpoccneql1b0krsle0llw37/gradle-8.11.1/bin/gradle"
  "$GRADLE" :app:assembleAlphaRelease -Pandroid.injected.build.abi=arm64-v8a --rerun-tasks \
    -Dhttp.proxyHost=127.0.0.1 -Dhttp.proxyPort=7897 \
    -Dhttps.proxyHost=127.0.0.1 -Dhttps.proxyPort=7897
fi

[ -f "$APK" ] || { echo "APK not found: $APK"; exit 1; }

SHA256=$(sha256sum "$APK" | awk '{print $1}')
mkdir -p build/release-meta
LATEST_JSON="build/release-meta/latest.json"
cat > "$LATEST_JSON" <<EOF
{"versionCode": $VERSION_CODE, "versionName": "$VERSION_NAME", "sha256": "$SHA256"}
EOF

if gh release view "$TAG" -R "$REPO" >/dev/null 2>&1; then
  gh release upload "$TAG" "$APK" "$LATEST_JSON" -R "$REPO" --clobber
else
  gh release create "$TAG" "$APK" "$LATEST_JSON" -R "$REPO" --title "$TAG" --generate-notes
fi
echo "published $TAG (versionCode=$VERSION_CODE sha256=$SHA256)"
