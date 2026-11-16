#!/usr/bin/env python3
"""
AWSB 响应采集脚本 — 向一个渠道发送全部用例请求，把原始响应记录到文件夹。

只做三件事: 发请求 → 落盘 → 打印进度。不做任何判断/断言。
每个用例的官方预期状态写在 cases.py 的 expect 注释里，随 meta.json 落盘，
供人工或后续对比脚本使用。

用法:
    python3 tests/awsb_cases/collect.py -c adm                 # 对 ADM 渠道跑全部用例
    python3 tests/awsb_cases/collect.py -c xdm                 # 对 XDM (真实 AWSB) 跑全部
    python3 tests/awsb_cases/collect.py -c adm --only 07_errors  # 只跑错误类
    python3 tests/awsb_cases/collect.py -c adm --only cache tools_roundtrip  # 多个过滤词
    python3 tests/awsb_cases/collect.py -c adm -m claude-haiku-4-5           # 换模型
    python3 tests/awsb_cases/collect.py -e URL -k KEY          # 自定义渠道
    python3 tests/awsb_cases/collect.py --list                 # 只列出用例，不发请求
    python3 tests/awsb_cases/collect.py -c adm --include-expensive  # 包含高成本用例

渠道预设从 tests/channel_probe.py 导入（本地文件，含密钥不提交）；
没有该文件时用 -e URL -k KEY 指定渠道。

输出目录结构 (tests/awsb_cases/results/<渠道>_<时间戳>/):
    run_config.json                  本次运行配置（key 已脱敏）
    index.csv                        汇总表: 序号,用例,分类,HTTP,耗时,官方预期,说明
    001_basic_minimal/
        request.json                 实际发送的 method/url/headers/body（key 脱敏）
        response_headers.json        全部响应头
        response_body.json           非流式响应体（JSON 美化；非 JSON 存 .txt）
        stream_raw.sse               流式原始 SSE 字节（逐字节保真，供指纹分析）
        stream_events.txt            流式逐行 + 相对时间戳（供人读）
        meta.json                    状态码/首字节耗时/总耗时/官方预期/说明
        step2_*                      二步用例的第二步（缓存重发/工具回传/thinking 回传）
"""

import argparse
import csv
import json
import sys
import time
import urllib.error
import urllib.request
from datetime import datetime
from pathlib import Path

from cases import build_cases

# ═══════════════════════════════════════════════════════════════
# 渠道预设：从 tests/channel_probe.py 导入（本地文件，含密钥，不提交）。
# 没有该文件时预设为空，用 -e/-k 传入自定义渠道即可运行。
# ═══════════════════════════════════════════════════════════════

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))
try:
    from channel_probe import CHANNELS  # noqa: E402
except ImportError:
    CHANNELS = {}

DEFAULT_MODEL = "claude-sonnet-4-6"


# ═══════════════════════════════════════════════════════════════
# 发送与落盘
# ═══════════════════════════════════════════════════════════════

def send_and_record(case, body, endpoint, key, out_dir, prefix, timeout):
    """发送一步请求并把原始响应写入 out_dir/<prefix>* 文件。

    返回 (status, resp_json_or_None, total_ms)。
    status 为 None 表示网络层失败（超时/连接错误），错误信息记入 meta。
    不做任何判断 —— HTTP 4xx/5xx 同样完整记录。
    """
    method = case.get("method", "POST")
    path = case.get("path", "/v1/messages")
    url = endpoint.rstrip("/") + path
    stream = bool(case.get("stream"))

    # 组装请求头。auth 用例故意用错 key / 不带 key。
    headers = {"Content-Type": "application/json", "anthropic-version": "2023-06-01"}
    auth = case.get("auth")
    if auth == "invalid":
        headers["x-api-key"] = "sk-invalid-key-for-collection-test"
    elif auth != "omit":
        headers["x-api-key"] = key
    headers.update(case.get("headers", {}))
    headers = {k: v for k, v in headers.items() if v != ""}  # 空值 = 删除该头

    # dict → JSON；str → 原样发送（非法 JSON 用例）；None → 无 body（GET）
    data = None
    if body is not None:
        data = body.encode("utf-8") if isinstance(body, str) \
            else json.dumps(body, ensure_ascii=False).encode("utf-8")

    # 记录实际发送的请求（key 脱敏）
    redacted = {k: ("***REDACTED***" if k.lower() in ("x-api-key", "authorization") else v)
                for k, v in headers.items()}
    write_json(out_dir / f"{prefix}request.json",
               {"method": method, "url": url, "headers": redacted, "body": body})

    meta = {"id": case["id"], "cat": case["cat"], "desc": case["desc"],
            "expect": case["expect"], "step": prefix.rstrip("_") or "step1"}
    req = urllib.request.Request(url, data=data, headers=headers, method=method)
    t0 = time.monotonic()
    resp = None
    try:
        try:
            resp = urllib.request.urlopen(req, timeout=timeout)
        except urllib.error.HTTPError as e:
            resp = e  # 4xx/5xx 也是正常采集对象
        status = resp.status
        meta["http_status"] = status
        meta["headers_ms"] = round((time.monotonic() - t0) * 1000)
        write_json(out_dir / f"{prefix}response_headers.json", dict(resp.headers))

        resp_json = None
        if stream and status == 200:
            # 流式: 逐行读取，记录每行相对时间戳；原始字节单独保真存一份
            raw = bytearray()
            lines = []
            first_byte_ms = None
            for line in resp:
                t_ms = round((time.monotonic() - t0) * 1000)
                if first_byte_ms is None:
                    first_byte_ms = t_ms
                raw += line
                lines.append(f"[+{t_ms:>7}ms] {line.decode('utf-8', errors='replace').rstrip(chr(10))}")
            meta["first_byte_ms"] = first_byte_ms
            (out_dir / f"{prefix}stream_raw.sse").write_bytes(bytes(raw))
            (out_dir / f"{prefix}stream_events.txt").write_text("\n".join(lines), encoding="utf-8")
        else:
            # 非流式（含流式请求的错误响应）: 整体读取
            body_bytes = resp.read()
            text = body_bytes.decode("utf-8", errors="replace")
            try:
                resp_json = json.loads(text)
                write_json(out_dir / f"{prefix}response_body.json", resp_json)
            except (json.JSONDecodeError, ValueError):
                (out_dir / f"{prefix}response_body.txt").write_text(text, encoding="utf-8")

        meta["total_ms"] = round((time.monotonic() - t0) * 1000)
        write_json(out_dir / f"{prefix}meta.json", meta)
        return status, resp_json, meta["total_ms"]

    except (urllib.error.URLError, TimeoutError, OSError) as e:
        # 网络层失败: 没有 HTTP 响应可记，只记错误
        meta["http_status"] = None
        meta["network_error"] = str(e)
        meta["total_ms"] = round((time.monotonic() - t0) * 1000)
        write_json(out_dir / f"{prefix}meta.json", meta)
        return None, None, meta["total_ms"]
    finally:
        if resp is not None:
            resp.close()


def write_json(path, obj):
    path.write_text(json.dumps(obj, indent=2, ensure_ascii=False), encoding="utf-8")


def run_case(case, endpoint, key, out_dir, timeout):
    """跑一个用例（含可选的第二步 followup），返回汇总行。"""
    status, resp_json, total_ms = send_and_record(
        case, case.get("body"), endpoint, key, out_dir, "", timeout)

    # 二步用例: 缓存重发 / 工具回传 / thinking 回传。
    # 第一步失败或没产生所需内容时 followup 返回 None，自动跳过。
    followup = case.get("followup")
    status2 = None
    if followup and resp_json is not None:
        body2 = followup(case["body"], resp_json)
        if body2 is not None:
            status2, _, ms2 = send_and_record(
                case, body2, endpoint, key, out_dir, "step2_", timeout)
            total_ms += ms2

    return {
        "id": case["id"], "cat": case["cat"],
        "http": status if status is not None else "NET_ERR",
        "http_step2": status2 if status2 is not None else "",
        "total_ms": total_ms, "expect": case["expect"], "desc": case["desc"],
    }


# ═══════════════════════════════════════════════════════════════
# 主流程
# ═══════════════════════════════════════════════════════════════

def main():
    parser = argparse.ArgumentParser(
        description="AWSB 响应采集: 发请求→存文件，不做判断",
        formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("-c", "--channel", default="adm",
                        help=f"渠道预设 ({', '.join(CHANNELS)}), 默认 adm")
    parser.add_argument("-e", "--endpoint", help="自定义 endpoint (覆盖 -c)")
    parser.add_argument("-k", "--key", help="自定义 API key (覆盖 -c)")
    parser.add_argument("-m", "--model", default=DEFAULT_MODEL,
                        help=f"模型, 默认 {DEFAULT_MODEL}")
    parser.add_argument("--only", nargs="*", default=None,
                        help="过滤词（匹配用例 id 或分类，可多个），如 --only 07_errors cache")
    parser.add_argument("--list", action="store_true", help="只列出用例，不发请求")
    parser.add_argument("--include-expensive", action="store_true",
                        help="包含高成本用例（上下文超长，约 $3+/次）")
    parser.add_argument("--timeout", type=int, default=180, help="单请求超时秒数, 默认 180")
    args = parser.parse_args()

    cases = build_cases(args.model, include_expensive=args.include_expensive)
    if args.only:
        cases = [c for c in cases
                 if any(w in c["id"] or w in c["cat"] for w in args.only)]
    if not cases:
        print("没有匹配的用例")
        sys.exit(1)

    if args.list:
        for i, c in enumerate(cases, 1):
            print(f"{i:>3}  {c['cat']:<16} {c['id']:<34} {c['desc']}")
        print(f"\n共 {len(cases)} 个用例")
        return

    # 解析渠道
    if args.endpoint and args.key:
        label, name, endpoint, key = "custom", "自定义渠道", args.endpoint, args.key
    elif args.channel in CHANNELS:
        ch = CHANNELS[args.channel]
        label, name, endpoint, key = args.channel, ch["name"], ch["endpoint"], ch["key"]
    else:
        print(f"未知渠道: {args.channel}，可用: {', '.join(CHANNELS)}")
        sys.exit(1)

    # 每次运行独立目录，互不覆盖
    stamp = datetime.now().strftime("%Y%m%d_%H%M%S")
    run_dir = Path(__file__).parent / "results" / f"{label}_{stamp}"
    run_dir.mkdir(parents=True)
    write_json(run_dir / "run_config.json", {
        "channel": name, "endpoint": endpoint, "model": args.model,
        "started_at": stamp, "case_count": len(cases),
        "filters": args.only, "include_expensive": args.include_expensive,
    })

    print(f"渠道: {name}\n地址: {endpoint}\n模型: {args.model}")
    print(f"用例: {len(cases)} 个\n输出: {run_dir}\n" + "=" * 72)

    rows = []
    for i, case in enumerate(cases, 1):
        case_dir = run_dir / f"{i:03d}_{case['id']}"
        case_dir.mkdir()
        row = run_case(case, endpoint, key, case_dir, args.timeout)
        rows.append(row)
        step2 = f" → step2 {row['http_step2']}" if row["http_step2"] != "" else ""
        print(f"[{i:>3}/{len(cases)}] {case['id']:<34} HTTP {row['http']}{step2}"
              f"  {row['total_ms'] / 1000:.1f}s")

    # 汇总表（expect 列即官方预期注释，人工对照用，脚本不判断）
    with open(run_dir / "index.csv", "w", newline="", encoding="utf-8-sig") as f:
        writer = csv.DictWriter(f, fieldnames=["id", "cat", "http", "http_step2",
                                               "total_ms", "expect", "desc"])
        writer.writeheader()
        writer.writerows(rows)

    print("=" * 72)
    print(f"完成 — {len(cases)} 个用例已记录到 {run_dir}")
    print(f"汇总: {run_dir / 'index.csv'}")


if __name__ == "__main__":
    main()