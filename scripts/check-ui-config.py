#!/usr/bin/env python3
"""校验 app/ui/config 的入口声明。

fnpack 要求 app/ui/config 的顶层是 {".url": {入口名: {...}}}；
入口名还必须与 manifest 的 desktop_applaunchname 一致，否则应用中心里点开是空白。
统一网关入口还必须有 gatewayPrefix + gatewaySocket。
"""

import json
import sys


def main() -> int:
    if len(sys.argv) != 3:
        print("用法: check-ui-config.py <app/ui/config> <desktop_applaunchname>")
        return 2

    path, want = sys.argv[1], sys.argv[2]

    try:
        with open(path, encoding="utf-8") as fh:
            cfg = json.load(fh)
    except OSError as exc:
        print(f"读取失败：{exc}")
        return 1
    except json.JSONDecodeError as exc:
        print(f"JSON 解析失败：{exc}")
        return 1

    urls = cfg.get(".url")
    if not isinstance(urls, dict) or not urls:
        print("缺少顶层 .url 声明")
        return 1

    names = ", ".join(urls.keys())
    if want not in urls:
        print(f"desktop_applaunchname={want} 与 ui/config 的入口 {names} 不一致")
        return 1

    app = urls[want]
    if not isinstance(app, dict):
        print(f"入口 {want} 的值不是对象")
        return 1

    for key in ("title", "icon", "type", "url"):
        if not app.get(key):
            print(f"入口 {want} 缺少 {key}")
            return 1

    missing = [k for k in ("gatewayPrefix", "gatewaySocket") if not app.get(k)]
    if missing:
        print(f"网关入口缺少 {' / '.join(missing)}")
        return 1

    prefix = app["gatewayPrefix"]
    if not prefix.startswith("/app/"):
        print(f"gatewayPrefix 应为 /app/{{appname}} 形式，实际 {prefix}")
        return 1

    if "/" in app["gatewaySocket"]:
        print(f"gatewaySocket 只填文件名，实际 {app['gatewaySocket']}")
        return 1

    print(f"入口 {want} · {app['type']} · {prefix} → {app['gatewaySocket']}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
