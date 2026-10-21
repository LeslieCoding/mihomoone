# MihomoOne

基于 [ClashMetaForAndroid](https://github.com/MetaCubeX/ClashMetaForAndroid) 裁剪二开的安卓代理客户端，内嵌 mihomo 内核（c-shared）。

与上游的主要差异：

- 自设计首页（状态主卡 / 当前出口实时节点 / 一键刷新 / 订阅）与亮蓝主题
- 代理页精简为「自动选择 + 节点选择」两组；自动选择全地区仅 Hysteria/Hysteria2 节点，url-test 主动探测
- 订阅下载校验 + 原子替换 + 三级网络回退（直连 → 127.0.0.1:7890 → LAN 代理）
- 整包自更新：启动时检查本仓库 Releases 的 `latest.json`，比对 versionCode → 下载 APK → SHA256 校验 → 弹窗安装
- 网络切换立即触发全量节点健康检查

## 发版

```bash
bash publish-release.sh
```

构建 APK、计算 SHA256、生成 `latest.json`、创建 GitHub Release 并上传产物。

## 构建

- JDK 21 / Android SDK 35 / NDK 29 / Go 1.26（定制 toolchain）
- `gradle :app:assembleAlphaRelease -Pandroid.injected.build.abi=arm64-v8a`
- geo 数据文件已存在于 `app/src/main/assets/` 时跳过重复下载

包名 `com.mihomoone.app`，最低 Android 5.0，推荐 Android 7.0+。
