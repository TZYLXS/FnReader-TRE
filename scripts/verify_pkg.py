#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""校验 dist 下 ipk/apk 的版本、架构与关键配置。"""
import io
import struct
import sys
import tarfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
DIST = ROOT / "dist"
EXPECT = "0.4"


def ar_members(path):
    data = path.read_bytes()
    if not data.startswith(b"!<arch>\n"):
        raise ValueError("不是 ar 归档")
    pos = 8
    while pos + 60 <= len(data):
        hdr = data[pos:pos + 60]
        name = hdr[0:16].decode("utf-8", "replace").strip().rstrip("/")
        size = int(hdr[48:58].decode("ascii").strip())
        body = data[pos + 60:pos + 60 + size]
        yield name, body
        pos += 60 + size + (size & 1)


def tar_text(blob, member):
    with tarfile.open(fileobj=io.BytesIO(blob), mode="r:gz") as tf:
        for m in tf.getmembers():
            if m.name.split("/")[-1] == member:
                return tf.extractfile(m).read().decode("utf-8", "replace")
    return ""


def elf_machine(path):
    data = path.read_bytes()[:20]
    if data[:4] != b"\x7fELF":
        return "not-elf"
    return {40: "ARM", 183: "AArch64"}.get(struct.unpack("<H", data[18:20])[0], "?")


def main():
    errors = []
    for arch in ("armv7", "arm64"):
        ipk = DIST / f"fnparse_{EXPECT}_{arch}.ipk"
        apk = DIST / f"fnparse_{EXPECT}_{arch}.apk"
        for p in (ipk, apk):
            if not p.exists():
                errors.append(f"缺少 {p.name}")
                continue
            if p.stat().st_size < 1_000_000:
                errors.append(f"{p.name} 体积异常 {p.stat().st_size}")

        if ipk.exists():
            control = ""
            for name, body in ar_members(ipk):
                if name.startswith("control.tar"):
                    control = tar_text(body, "control")
            ver = [l for l in control.splitlines() if l.startswith("Version:")]
            dep = [l for l in control.splitlines() if l.startswith("Depends:")]
            print(f"[ipk] {ipk.name} {ver[0] if ver else 'NO-VERSION'} {dep[0] if dep else ''}")
            if not ver or f"Version: {EXPECT}" not in ver[0]:
                errors.append(f"{ipk.name} 版本不是 {EXPECT}")

        if apk.exists():
            info = ""
            with tarfile.open(apk, "r:gz") as tf:
                for m in tf.getmembers():
                    if m.name.split("/")[-1] in (".PKGINFO", "control"):
                        info = tf.extractfile(m).read().decode("utf-8", "replace")
                        break
            lines = [l for l in info.splitlines() if l.startswith("pkgver") or l.startswith("Version")]
            print(f"[apk] {apk.name} {lines[0] if lines else 'NO-PKGVER'}")
            if not lines or EXPECT not in lines[0]:
                errors.append(f"{apk.name} 版本不是 {EXPECT}")

    for binname, want in (("fnparse-linux-armv7", "ARM"), ("fnparse-linux-arm64", "AArch64")):
        p = DIST / "bin" / binname
        got = elf_machine(p) if p.exists() else "missing"
        print(f"[elf] {binname} machine={got}")
        if got != want:
            errors.append(f"{binname} 架构 {got} != {want}")

    cfg = DIST / "stage" / "armv7" / "etc" / "config" / "fnparse"
    settings = DIST / "stage" / "armv7" / "www" / "luci-static" / "resources" / "view" / "fnparse" / "settings.js"
    if cfg.exists():
        text = cfg.read_text(encoding="utf-8", errors="replace")
        print("[cfg] claim_types=" + next((l for l in text.splitlines() if "claim_types" in l), "MISSING"))
        if "cover" not in text or "bookinfo" not in text:
            errors.append("配置未包含 cover/bookinfo")
    if settings.exists():
        text = settings.read_text(encoding="utf-8", errors="replace")
        if "cover,intro,bookinfo" not in text:
            errors.append("LuCI settings.js 未更新媒体任务")

    if errors:
        print("\nFAIL:")
        for e in errors:
            print(" - " + e)
        return 1
    print("\nOK: 版本、架构、配置全部通过")
    return 0


if __name__ == "__main__":
    sys.exit(main())
