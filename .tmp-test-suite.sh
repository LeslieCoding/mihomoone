#!/bin/bash
# ProxyOne CMFA 平板测试套件 —— PC 侧总控（Git Bash）
# 平板 curl 走 TUN（真实 VPN 路径）；结果输出到 /d/Software/ClashMetaForAndroid/.tmp-test-results.log
set -u
ADB="adb -s 2d64f765"
LOG=/d/Software/ClashMetaForAndroid/.tmp-test-results.log
: > "$LOG"
say(){ echo "[$(date +%H:%M:%S)] $*" | tee -a "$LOG"; }
export MSYS_NO_PATHCONV=1

# ---------- 0. 前置确认 ----------
$ADB shell echo ok >/dev/null 2>&1 || { say "FATAL: device offline"; exit 1; }

# ---------- 1. WiFi 开关 10 次恢复测试 ----------
say "=== T1 WiFi toggle x10 ==="
pass=0
for i in $(seq 1 10); do
  $ADB shell svc wifi disable; sleep 3
  $ADB shell svc wifi enable
  T0=$(date +%s%3N); ok=0
  for w in $(seq 1 15); do
    R=$($ADB shell "curl -s -o /dev/null --max-time 5 -w '%{http_code}' https://www.google.com/generate_204" 2>/dev/null)
    if [ "$R" = "204" ]; then ok=1; break; fi
    sleep 2
  done
  T1=$(date +%s%3N)
  say "toggle#$i recover=$ok time=$(( (T1-T0)/1000 ))s"
  [ "$ok" = "1" ] && pass=$((pass+1))
done
say "T1 RESULT: $pass/10 pass"

# ---------- 2. 并发 20 请求 ----------
say "=== T2 concurrency x20 ==="
$ADB shell 'rm -f /data/local/tmp/cc_*.r; for i in $(seq 1 20); do (curl -s -o /dev/null --max-time 20 -w "%{http_code}" "https://www.cloudflare.com/cdn-cgi/trace?c=$i" > /data/local/tmp/cc_$i.r) & done; wait' 2>/dev/null
OK=$($ADB shell "cat /data/local/tmp/cc_*.r 2>/dev/null | grep -c 200")
say "T2 RESULT: $OK/20 http200"
$ADB shell "rm -f /data/local/tmp/cc_*.r"

# ---------- 3. 吞吐三模式 ----------
say "=== T3 throughput ==="
# 3a. LAN 直连基线（不过 TUN，纯 WiFi+网卡能力）
for i in 1 2 3; do
  R=$($ADB shell "curl -s -o /dev/null --max-time 60 -w '%{speed_download}' http://192.168.5.6:8788/100mb.bin" 2>/dev/null)
  say "T3a LAN-direct run$i: $(echo "$R" | awk '{printf "%.1f Mbps", $1*8/1000000}')"
done
# 3b. VPN 代理路径（公网 ovh）
for i in 1 2 3; do
  R=$($ADB shell "curl -s -o /dev/null --max-time 120 -w '%{speed_download}' https://proof.ovh.net/files/100Mb.dat" 2>/dev/null)
  say "T3b VPN-proxy run$i: $(echo "$R" | awk '{printf "%.1f Mbps", $1*8/1000000}')"
done
# 3c. VPN 开·国内直连规则（百度资源）
for i in 1 2 3; do
  R=$($ADB shell "curl -s -o /dev/null --max-time 60 -w '%{speed_download}' https://dldir1.qq.com/qqfile/qq/PCQQ9.7.17/QQ9.7.17.29225.exe -r 0-20971519" 2>/dev/null)
  say "T3c VPN-cn-direct(20MB) run$i: $(echo "$R" | awk '{printf "%.1f Mbps", $1*8/1000000}')"
done

# ---------- 4. 30 分钟持续成功率 + 延迟采样 ----------
say "=== T4 30min sustained (5s interval, alternating google/baidu) ==="
$ADB shell 'rm -f /data/local/tmp/t4.log; nohup sh -c '"'"'
i=0
while [ $i -lt 360 ]; do
  if [ $((i % 2)) -eq 0 ]; then U="https://www.google.com/generate_204?t=$i"; else U="https://www.baidu.com/?t=$i"; fi
  R=$(curl -s -o /dev/null --max-time 8 -w "%{http_code} %{time_total}" "$U" 2>/dev/null)
  echo "$i $R" >> /data/local/tmp/t4.log
  i=$((i+1))
  sleep 5
done'"'"' > /dev/null 2>&1 &' 
sleep 3
$ADB shell "pgrep -f t4.log >/dev/null && echo runner-alive || echo runner-dead" | tee -a "$LOG"

# 期间每 5 分钟采一次内存/CPU
for m in 0 5 10 15 20 25; do
  if [ "$m" != "0" ]; then sleep 300; fi
  PSS=$($ADB shell "dumpsys meminfo com.proxyone.app:background 2>/dev/null | grep 'TOTAL PSS' | awk '{print \$3}'")
  CPU=$($ADB shell "top -b -n 1 2>/dev/null | grep proxyone | head -2 | awk '{print \$9}'" | paste -sd+)
  DONE=$($ADB shell "wc -l < /data/local/tmp/t4.log 2>/dev/null" || echo 0)
  say "T4 sample@${m}min: requests_done=$DONE bg_PSS_KB=$PSS cpu%=$CPU"
done

# 汇总 T4
$ADB shell "cat /data/local/tmp/t4.log" > /d/Software/ClashMetaForAndroid/.tmp-t4.log 2>/dev/null
python - <<'EOF' | tee -a "$LOG"
import re
rows=[l.split() for l in open(r'D:\Software\ClashMetaForAndroid\.tmp-test-results.log',encoding='utf-8',errors='ignore') if 0]
data=[l.split() for l in open(r'D:\Software\ClashMetaForAndroid\.tmp-t4.log',encoding='utf-8',errors='ignore') if len(l.split())>=3]
g=[(float(r[2]),r[1]) for r in data if int(r[0])%2==0]
b=[(float(r[2]),r[1]) for r in data if int(r[0])%2==1]
def stat(name,d,okcode):
    if not d: print(f"{name}: no data"); return
    ok=[t for t,c in d if c==okcode]; fail=len(d)-len(ok)
    ts=sorted(t for t,c in d if c==okcode) or [0]
    p50=ts[len(ts)//2]; p95=ts[int(len(ts)*0.95)]
    print(f"{name}: n={len(d)} ok={len(ok)} fail={fail} rate={len(ok)/len(d)*100:.1f}% p50={p50*1000:.0f}ms p95={p95*1000:.0f}ms")
stat("T4 google(proxy)", g, "204")
stat("T4 baidu(direct)", b, "200")
EOF
say "=== SUITE DONE ==="
