#!/usr/bin/env python3
"""
AWSB 采集结果对比页生成器 — 把两个测试目录的内容提取进一个独立 HTML。

用法:
    python3 tests/awsb_cases/build_compare.py <目录A> <目录B> [-o 输出.html]

示例:
    python3 tests/awsb_cases/build_compare.py \
        results/adm_20261116_090716 results/xdm_20261116_080444 \
        -o compare_adm_vs_xdm.html

生成的 HTML 无外部依赖、无需服务器，浏览器直接打开:
    - 左右两栏并排显示 A/B 两次运行的请求、响应头、响应体、流式事件、第二步
    - 响应体 JSON 美化 + 逐行差异高亮（键顺序差异也能看出）
    - 左侧用例列表带双方状态徽章，可搜索、只看状态差异
    - 顶部 ⇄ 按钮交换左右两侧

数据在采集时已脱敏（request 里的 x-api-key 为 ***REDACTED***）。
"""

import argparse
import csv
import json
import re
from pathlib import Path


def load_run(run_dir):
    """提取一个运行目录: index.csv 汇总 + 每用例的全部采集文件。"""
    run_dir = Path(run_dir)
    with open(run_dir / "index.csv", encoding="utf-8-sig") as f:
        summary = list(csv.DictReader(f))

    cfg = {}
    cfg_file = run_dir / "run_config.json"
    if cfg_file.exists():
        cfg = json.loads(cfg_file.read_text(encoding="utf-8"))

    cases = {}
    for d in sorted(run_dir.iterdir()):
        if not d.is_dir() or not re.match(r"^\d{3}_", d.name):
            continue
        cid = d.name.split("_", 1)[1]

        def load(prefix):
            step = {}
            for key, fname in [("request", "request.json"),
                               ("headers", "response_headers.json"),
                               ("meta", "meta.json")]:
                f = d / f"{prefix}{fname}"
                if f.exists():
                    step[key] = json.loads(f.read_text(encoding="utf-8"))
            body_json = d / f"{prefix}response_body.json"
            body_txt = d / f"{prefix}response_body.txt"
            if body_json.exists():
                step["body"] = json.loads(body_json.read_text(encoding="utf-8"))
                step["body_is_json"] = True
            elif body_txt.exists():
                step["body"] = body_txt.read_text(encoding="utf-8")
                step["body_is_json"] = False
            stream = d / f"{prefix}stream_events.txt"
            if stream.exists():
                step["stream"] = stream.read_text(encoding="utf-8")
            return step or None

        cases[cid] = {"step1": load(""), "step2": load("step2_")}

    return {"name": run_dir.name, "channel": cfg.get("channel", run_dir.name),
            "model": cfg.get("model", ""), "summary": summary, "cases": cases}


def main():
    parser = argparse.ArgumentParser(description="生成两次采集运行的对比 HTML")
    parser.add_argument("dir_a", help="运行目录 A（左侧）")
    parser.add_argument("dir_b", help="运行目录 B（右侧）")
    parser.add_argument("-o", "--output", default="compare.html", help="输出 HTML 路径")
    args = parser.parse_args()

    data = {"A": load_run(args.dir_a), "B": load_run(args.dir_b)}
    payload = json.dumps(data, ensure_ascii=False, separators=(",", ":"))
    # </script> 会提前闭合脚本标签，转义掉
    payload = payload.replace("</", "<\\/")
    html = PAGE.replace("__DATA__", payload)

    out = Path(args.output)
    out.write_text(html, encoding="utf-8")
    print(f"A: {data['A']['name']} ({len(data['A']['cases'])} 用例)")
    print(f"B: {data['B']['name']} ({len(data['B']['cases'])} 用例)")
    print(f"已生成 {out} ({out.stat().st_size / 1024 / 1024:.1f} MB)，浏览器直接打开")


PAGE = r"""<!doctype html>
<html lang="zh">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>AWSB 采集对比</title>
<style>
  :root {
    --bg: #f6f7f9; --panel: #ffffff; --border: #d9dde3; --text: #1c2733;
    --muted: #6b7684; --accent: #2563eb; --ok: #16a34a; --err: #dc2626;
    --warn: #d97706; --diff-a: #fde8e8; --diff-b: #e6f4ea; --code-bg: #f2f4f7;
  }
  @media (prefers-color-scheme: dark) {
    :root {
      --bg: #14181d; --panel: #1c2128; --border: #333a44; --text: #d7dde4;
      --muted: #8b95a1; --accent: #6ea8fe; --ok: #4ade80; --err: #f87171;
      --warn: #fbbf24; --diff-a: #4a2426; --diff-b: #1f3a2a; --code-bg: #22272e;
    }
  }
  * { box-sizing: border-box; }
  body { margin: 0; font: 14px/1.5 system-ui, "PingFang SC", "Microsoft YaHei", sans-serif;
         background: var(--bg); color: var(--text); }
  header { position: sticky; top: 0; z-index: 5; background: var(--panel);
           border-bottom: 1px solid var(--border); padding: 10px 16px;
           display: flex; gap: 10px; align-items: center; flex-wrap: wrap; }
  header h1 { font-size: 15px; margin: 0 12px 0 0; }
  .runlabel { font-family: ui-monospace, Consolas, monospace; font-size: 12.5px;
              padding: 4px 8px; border: 1px solid var(--border); border-radius: 6px;
              max-width: 340px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  button { padding: 5px 10px; border: 1px solid var(--border); border-radius: 6px;
           background: var(--panel); color: var(--text); cursor: pointer; }
  button:hover { border-color: var(--accent); }
  label.chk { color: var(--muted); display: flex; align-items: center; gap: 4px; }
  #layout { display: grid; grid-template-columns: 300px 1fr; height: calc(100vh - 53px); }
  #sidebar { border-right: 1px solid var(--border); overflow-y: auto; background: var(--panel); }
  #search { width: calc(100% - 16px); margin: 8px; padding: 6px 8px;
            border: 1px solid var(--border); border-radius: 6px;
            background: var(--bg); color: var(--text); }
  .case { padding: 7px 10px; border-bottom: 1px solid var(--border); cursor: pointer; }
  .case:hover { background: var(--bg); }
  .case.active { background: var(--bg); border-left: 3px solid var(--accent); padding-left: 7px; }
  .case .cid { font-family: ui-monospace, Consolas, monospace; font-size: 12.5px; word-break: break-all; }
  .case .desc { color: var(--muted); font-size: 12px; }
  .badge { display: inline-block; min-width: 34px; text-align: center; padding: 0 5px;
           border-radius: 4px; font-size: 11.5px; font-weight: 600; margin-right: 4px;
           font-family: ui-monospace, Consolas, monospace; }
  .b2xx { background: color-mix(in srgb, var(--ok) 18%, transparent); color: var(--ok); }
  .b4xx { background: color-mix(in srgb, var(--warn) 20%, transparent); color: var(--warn); }
  .b5xx, .berr { background: color-mix(in srgb, var(--err) 18%, transparent); color: var(--err); }
  .mismatch { color: var(--err); font-weight: 700; margin-left: 4px; }
  #main { overflow-y: auto; padding: 12px 16px; }
  #casehead { margin-bottom: 10px; }
  #casehead h2 { font-size: 15px; margin: 0 0 4px; font-family: ui-monospace, Consolas, monospace; }
  #expect { color: var(--muted); font-size: 12.5px; white-space: pre-wrap; }
  #cols { display: grid; grid-template-columns: 1fr 1fr; gap: 14px; align-items: start; }
  .col { background: var(--panel); border: 1px solid var(--border); border-radius: 8px;
         padding: 10px 12px; min-width: 0; }
  .col h3 { margin: 0 0 8px; font-size: 13px; color: var(--accent); word-break: break-all; }
  details { margin: 6px 0; border: 1px solid var(--border); border-radius: 6px; }
  summary { cursor: pointer; padding: 6px 10px; font-size: 12.5px; font-weight: 600;
            color: var(--muted); user-select: none; }
  details[open] summary { border-bottom: 1px solid var(--border); }
  pre { margin: 0; padding: 8px 10px; overflow-x: auto; background: var(--code-bg);
        font: 12px/1.55 ui-monospace, Consolas, monospace; white-space: pre; }
  pre .da { background: var(--diff-a); display: inline-block; width: 100%; }
  pre .db { background: var(--diff-b); display: inline-block; width: 100%; }
  .empty { color: var(--muted); padding: 8px 10px; font-size: 12.5px; }
  .timing { color: var(--muted); font-size: 12px; margin-left: 6px; }
  @media (max-width: 900px) {
    #layout { grid-template-columns: 1fr; height: auto; }
    #sidebar { max-height: 40vh; }
    #cols { grid-template-columns: 1fr; }
  }
</style>
</head>
<body>
<header>
  <h1>AWSB 采集对比</h1>
  <span class="runlabel" id="labelA"></span>
  <button id="swap" title="交换左右">⇄</button>
  <span class="runlabel" id="labelB"></span>
  <label class="chk"><input type="checkbox" id="onlyDiff">只看状态差异</label>
  <span id="stat" style="color:var(--muted);font-size:12.5px"></span>
</header>
<div id="layout">
  <div id="sidebar">
    <input id="search" placeholder="搜索用例 id / 说明…">
    <div id="cases"></div>
  </div>
  <div id="main">
    <div id="casehead"><h2 id="caseTitle">← 选择用例</h2><div id="expect"></div></div>
    <div id="cols"><div class="col" id="colA"></div><div class="col" id="colB"></div></div>
  </div>
</div>
<script>
"use strict";
const DATA = __DATA__;
const $ = id => document.getElementById(id);
// 左右两侧当前指向的数据源（"A"/"B"），⇄ 交换的就是它
let sides = ["A", "B"];
let curCase = null;

const esc = s => String(s).replace(/[&<>"]/g, c => ({"&":"&amp;","<":"&lt;",">":"&gt;",'"':"&quot;"}[c]));
const badge = s => {
  if (s === "" || s == null) return "";
  const n = parseInt(s, 10);
  const cls = isNaN(n) ? "berr" : n < 300 ? "b2xx" : n < 500 ? "b4xx" : "b5xx";
  return `<span class="badge ${cls}">${esc(s)}</span>`;
};
const statusPair = r => r ? (r.http + (r.http_step2 ? "→" + r.http_step2 : "")) : "—";
const isMismatch = (a, b) => a && b && (a.http !== b.http ||
  (a.http_step2 && b.http_step2 && a.http_step2 !== b.http_step2));
const summary = k => DATA[k].summary;
const rowOf = (k, cid) => summary(k).find(r => r.id === cid);

// ── 逐行 diff：LCS 标记两侧各自独有的行（JSON 美化后逐行比对）──
function diffMarks(aLines, bLines) {
  const n = aLines.length, m = bLines.length;
  if (n * m > 4_000_000) return [new Set(), new Set()];  // 超大文件退化为不高亮
  const dp = Array.from({length: n + 1}, () => new Uint32Array(m + 1));
  for (let i = n - 1; i >= 0; i--)
    for (let j = m - 1; j >= 0; j--)
      dp[i][j] = aLines[i] === bLines[j] ? dp[i+1][j+1] + 1 : Math.max(dp[i+1][j], dp[i][j+1]);
  const ma = new Set(), mb = new Set();
  let i = 0, j = 0;
  while (i < n && j < m) {
    if (aLines[i] === bLines[j]) { i++; j++; }
    else if (dp[i+1][j] >= dp[i][j+1]) { ma.add(i); i++; }
    else { mb.add(j); j++; }
  }
  while (i < n) ma.add(i++);
  while (j < m) mb.add(j++);
  return [ma, mb];
}

function renderPre(text, marks, cls) {
  return "<pre>" + text.split("\n").map((l, i) =>
    marks && marks.has(i) ? `<span class="${cls}">${esc(l) || " "}</span>` : esc(l)
  ).join("\n") + "</pre>";
}

const section = (title, inner, open) =>
  `<details ${open ? "open" : ""}><summary>${title}</summary>${inner}</details>`;

// ── 渲染单侧一个 step 的全部采集内容 ──
function renderStep(step, other, side) {
  if (!step) return '<div class="empty">该运行无此步骤</div>';
  let html = "";
  const meta = step.meta || {};
  if (meta.http_status != null || meta.network_error) {
    html += `<div>${badge(meta.http_status ?? "NET_ERR")}
      <span class="timing">首字节 ${meta.first_byte_ms ?? meta.headers_ms ?? "?"}ms / 总 ${meta.total_ms ?? "?"}ms</span></div>`;
  }
  if (meta.network_error) html += `<div class="empty">网络错误: ${esc(meta.network_error)}</div>`;

  if (step.body !== undefined) {
    let mine, theirs = null;
    if (step.body_is_json) {
      mine = JSON.stringify(step.body, null, 2);
      if (other && other.body_is_json) theirs = JSON.stringify(other.body, null, 2);
    } else { mine = String(step.body); }
    let marks = null;
    if (theirs !== null) {
      const [ma, mb] = diffMarks(mine.split("\n"), theirs.split("\n"));
      marks = side === 0 ? ma : mb;
    }
    html += section("响应体" + (step.body_is_json ? "（JSON，差异高亮）" : "（原始文本）"),
                    renderPre(mine, marks, side === 0 ? "da" : "db"), true);
  }
  if (step.stream) html += section("流式事件（含相对时间戳）", renderPre(step.stream, null, ""), true);
  if (step.request) {
    const r = step.request;
    const reqText = `${r.method} ${r.url}\n\n` +
      Object.entries(r.headers || {}).map(([k, v]) => `${k}: ${v}`).join("\n") +
      "\n\n" + (typeof r.body === "string" ? r.body : JSON.stringify(r.body, null, 2));
    html += section("请求", renderPre(reqText, null, ""), false);
  }
  if (step.headers) {
    const h = Object.entries(step.headers).sort().map(([k, v]) => `${k}: ${v}`).join("\n");
    html += section("响应头", renderPre(h, null, ""), false);
  }
  return html || '<div class="empty">无采集文件</div>';
}

function showCase(cid) {
  curCase = cid;
  document.querySelectorAll(".case").forEach(el =>
    el.classList.toggle("active", el.dataset.cid === cid));
  const ra = rowOf(sides[0], cid), rb = rowOf(sides[1], cid);
  $("caseTitle").textContent = cid;
  $("expect").textContent = "官方预期: " + ((ra && ra.expect) || (rb && rb.expect) || "—");
  sides.forEach((k, idx) => {
    const data = DATA[k].cases[cid];
    const other = DATA[sides[1 - idx]].cases[cid];
    let html = `<h3>${idx === 0 ? "左" : "右"} · ${esc(DATA[k].name)}</h3>`;
    if (!data) html += '<div class="empty">该运行无此用例</div>';
    else {
      html += renderStep(data.step1, other && other.step1, idx);
      if (data.step2) {
        html += `<details open><summary style="font-weight:700;color:var(--accent)">第二步（回传/重发）</summary>` +
                renderStep(data.step2, other && other.step2, idx) + `</details>`;
      }
    }
    $(idx === 0 ? "colA" : "colB").innerHTML = html;
  });
}

function renderCases() {
  const q = $("search").value.trim().toLowerCase();
  const onlyDiff = $("onlyDiff").checked;
  const [ka, kb] = sides;
  const ids = [...new Set([...summary(ka).map(r => r.id), ...summary(kb).map(r => r.id)])];
  let shown = 0, html = "";
  for (const cid of ids) {
    const a = rowOf(ka, cid), b = rowOf(kb, cid);
    const mm = isMismatch(a, b);
    if (onlyDiff && !mm) continue;
    const desc = (a && a.desc) || (b && b.desc) || "";
    if (q && !cid.toLowerCase().includes(q) && !desc.toLowerCase().includes(q)) continue;
    shown++;
    html += `<div class="case ${cid === curCase ? "active" : ""}" data-cid="${esc(cid)}">
      <div>${badge(a ? statusPair(a) : "—")}${badge(b ? statusPair(b) : "—")}${mm ? '<span class="mismatch">✗</span>' : ""}</div>
      <div class="cid">${esc(cid)}</div><div class="desc">${esc(desc)}</div></div>`;
  }
  $("cases").innerHTML = html;
  const mmCount = ids.filter(cid => isMismatch(rowOf(ka, cid), rowOf(kb, cid))).length;
  $("stat").textContent = `${shown}/${ids.length} 用例 · 状态差异 ${mmCount} 个`;
  document.querySelectorAll(".case").forEach(el =>
    el.addEventListener("click", () => showCase(el.dataset.cid)));
}

function renderLabels() {
  $("labelA").textContent = "左: " + DATA[sides[0]].name;
  $("labelB").textContent = "右: " + DATA[sides[1]].name;
  $("labelA").title = DATA[sides[0]].channel;
  $("labelB").title = DATA[sides[1]].channel;
}

$("swap").addEventListener("click", () => {
  sides = [sides[1], sides[0]];
  renderLabels(); renderCases();
  if (curCase) showCase(curCase);
});
$("search").addEventListener("input", renderCases);
$("onlyDiff").addEventListener("change", renderCases);
renderLabels();
renderCases();
</script>
</body>
</html>
"""


if __name__ == "__main__":
    main()