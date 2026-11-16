#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""构建 OpenWrt / LEDE 安装包（.ipk 与 .apk）。

用法：
    python scripts/build_pkg.py            # 用 dist/bin 下已交叉编译好的二进制打包
    python scripts/build_pkg.py --build    # 先调 go 交叉编译再打包

产物：
    dist/fnparse_0.2_armv7.ipk
    dist/fnparse_0.2_arm64.ipk
    dist/fnparse_0.2_armv7.apk
    dist/fnparse_0.2_arm64.apk
    dist/SHA256SUMS
"""

import argparse
import gzip
import hashlib
import io
import os
import shutil
import subprocess
import sys
import tarfile
import time
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
DIST = ROOT / "dist"
FILES = ROOT / "openwrt" / "files"
LUCI = ROOT / "openwrt" / "luci-app-fnparse"
ENGINE = ROOT / "engine"

PKG_NAME = "luci-app-fnparse"
PKG_VERSION = "0.2"
PKG_RELEASE = "1"
MAINTAINER = "FnReader <fnreader@local>"
DESCRIPTION = ("FnReader 软总线解析引擎：长轮询领取 ebook-parse / ebook-chapter / cover / intro / "
               "bookinfo 任务，下载原书或详情页后按协议回传；LuCI 管理，解析缓存放 USB。")

ARCHS = [
    {
        "name": "armv7",
        "binary": "fnparse-linux-armv7",
        "ipk_arch": "arm_cortex-a7_neon-vfpv4",
        "apk_arch": "arm_cortex-a7_neon-vfpv4",
        "goarch": "arm",
        "goarm": "7",
    },
    {
        "name": "arm64",
        "binary": "fnparse-linux-arm64",
        "ipk_arch": "aarch64_cortex-a53",
        "apk_arch": "aarch64_cortex-a53",
        "goarch": "arm64",
        "goarm": "",
    },
]


def log(msg):
    print("[build] " + msg, flush=True)


def sha256_file(p):
    h = hashlib.sha256()
    with open(p, "rb") as f:
        for chunk in iter(lambda: f.read(1 << 20), b""):
            h.update(chunk)
    return h.hexdigest()


def build_go_binaries():
    """交叉编译 linux/arm、linux/arm64（纯静态，CGO 关闭）。"""
    go = shutil.which("go") or str(ROOT.parent / "tools" / "go" / "bin" / "go.exe")
    if not Path(go).exists():
        raise SystemExit("找不到 go，可先设置 PATH 或把工具链放在 D:\\iot\\tools\\go")
    env = dict(os.environ)
    env.setdefault("GOROOT", str(Path(go).resolve().parents[1]))
    env.setdefault("GOCACHE", str(Path(go).resolve().parents[1] / ".." / "gocache"))
    env.setdefault("TMP", str(Path(go).resolve().parents[1] / ".." / "tmp"))
    env.setdefault("TEMP", env["TMP"])
    env["CGO_ENABLED"] = "0"
    env["GOOS"] = "linux"
    (DIST / "bin").mkdir(parents=True, exist_ok=True)
    for a in ARCHS:
        out = DIST / "bin" / a["binary"]
        e = dict(env)
        e["GOARCH"] = a["goarch"]
        if a["goarm"]:
            e["GOARM"] = a["goarm"]
        cmd = [go, "build", "-trimpath", "-ldflags", "-s -w", "-o", str(out), "."]
        log("编译 %s：GOARCH=%s" % (a["name"], a["goarch"]))
        subprocess.check_call(cmd, cwd=str(ENGINE), env=e)
    log("Go 交叉编译完成")


def copytree(src, dst):
    """把目录内容合并进 dst（保留可执行位）。"""
    for base, dirs, files in os.walk(src):
        rel = os.path.relpath(base, src)
        target = dst if rel == "." else dst / rel
        target.mkdir(parents=True, exist_ok=True)
        for f in files:
            s = Path(base) / f
            t = target / f
            shutil.copy2(s, t)
            mode = s.stat().st_mode & 0o777
            t.chmod(mode)


def stage_tree(arch):
    stage = DIST / "stage" / arch["name"]
    if stage.exists():
        shutil.rmtree(stage)
    stage.mkdir(parents=True)
    copytree(FILES, stage)
    copytree(LUCI / "root", stage)
    # LuCI 静态资源：htdocs/luci-static -> /www/luci-static
    static_src = LUCI / "htdocs" / "luci-static"
    if static_src.exists():
        copytree(static_src, stage / "www" / "luci-static")

    bindir = stage / "usr" / "bin"
    bindir.mkdir(parents=True, exist_ok=True)
    shutil.copy2(DIST / "bin" / arch["binary"], bindir / "fnparse")
    (bindir / "fnparse").chmod(0o755)
    for script in [
        stage / "etc" / "init.d" / "fnparse",
        stage / "usr" / "libexec" / "rpcd" / "fnparse",
        stage / "etc" / "uci-defaults" / "95-luci-app-fnparse",
    ]:
        if script.exists():
            script.chmod(0o755)
    (stage / "etc" / "config" / "fnparse").chmod(0o600)
    return stage


def all_paths(stage):
    """返回 (相对路径, 绝对路径, 是否目录)。"""
    out = []
    for base, dirs, files in os.walk(stage):
        dirs.sort()
        relbase = Path(os.path.relpath(base, stage))
        if str(relbase) != ".":
            out.append((relbase, Path(base), True))
        for f in sorted(files):
            out.append((relbase / f, Path(base) / f, False))
    return out


def tar_bytes(entries, gz=True, mtime=None):
    """entries: [(arcname, source_path_or_bytes, mode)]"""
    buf = io.BytesIO()
    mode = "w:gz" if gz else "w"
    with tarfile.open(fileobj=buf, mode=mode, format=tarfile.GNU_FORMAT) as tf:
        for arc, src, fmode in entries:
            info = tarfile.TarInfo(arc)
            info.mode = fmode
            info.uid = 0
            info.gid = 0
            info.uname = "root"
            info.gname = "root"
            info.mtime = int(mtime or time.time())
            if isinstance(src, bytes):
                data = src
                info.size = len(data)
                tf.addfile(info, io.BytesIO(data))
            else:
                data = Path(src).read_bytes()
                info.size = len(data)
                tf.addfile(info, io.BytesIO(data))
    return buf.getvalue()


def data_tar(stage, mtime=None):
    entries = [(".", b"", 0o755)]
    for rel, p, isdir in all_paths(stage):
        arc = "./" + str(rel).replace("\\", "/")
        if isdir:
            entries.append((arc, b"", 0o755))
        else:
            fmode = p.stat().st_mode & 0o777
            entries.append((arc, p, fmode))
    return tar_bytes(entries, gz=True, mtime=mtime)


def dir_size(stage):
    total = 0
    for rel, p, isdir in all_paths(stage):
        if not isdir:
            total += p.stat().st_size
    return total


def write_ar(members, out):
    """写 Debian/opkg 的 ar 归档。members: [(name, bytes)]"""
    with open(out, "wb") as f:
        f.write(b"!<arch>\n")
        for name, data in members:
            name_field = (name + "/").ljust(16)[:16]
            mtime = str(int(time.time())).ljust(12)[:12]
            uid = "0".ljust(6)
            gid = "0".ljust(6)
            fmode = "100644".ljust(8)
            size = str(len(data)).ljust(10)[:10]
            hdr = (name_field + mtime + uid + gid + fmode + size + "`\n").encode()
            f.write(hdr)
            f.write(data)
            if len(data) % 2 == 1:
                f.write(b"\n")


def control_text(arch, size_kb):
    return """Package: {pkg}
Version: {ver}
Depends: luci-base
Provides: fnparse
Section: utils
Priority: optional
Architecture: {arch}
Installed-Size: {size}
Maintainer: {maint}
Description: {desc}
 长轮询 /api/m/remote/pending 领取任务，下载原书后解析成 fnreader.ebook.v1 分片回传。
 纯静态 Go 二进制，安装包自带运行环境；解析缓存建议放 USB，LuCI 里可配置接入 IP/Token。
""".format(pkg=PKG_NAME, ver=PKG_VERSION, arch=arch["ipk_arch"],
           size=max(1, size_kb),
           maint=MAINTAINER, desc=DESCRIPTION)


POSTINST = """#!/bin/sh
# postinst: 注册开机启动
[ -n "$IPKG_INSTROOT" ] && exit 0
if [ -x /etc/init.d/fnparse ]; then
	/etc/init.d/fnparse enable >/dev/null 2>&1
	/etc/init.d/fnparse restart >/dev/null 2>&1
fi
exit 0
"""

PRERM = """#!/bin/sh
[ -n "$IPKG_INSTROOT" ] && exit 0
if [ -x /etc/init.d/fnparse ]; then
	/etc/init.d/fnparse stop >/dev/null 2>&1
fi
exit 0
"""

POSTRM = """#!/bin/sh
[ -n "$IPKG_INSTROOT" ] && exit 0
if [ -x /etc/init.d/fnparse ]; then
	/etc/init.d/fnparse disable >/dev/null 2>&1
fi
exit 0
"""


def build_ipk(arch):
    stage = stage_tree(arch)
    size_kb = max(1, dir_size(stage) // 1024)
    data = data_tar(stage)
    ctrl_entries = [
        ("./control", control_text(arch, size_kb).encode(), 0o644),
        ("./postinst", POSTINST.encode(), 0o755),
        ("./prerm", PRERM.encode(), 0o755),
        ("./postrm", POSTRM.encode(), 0o755),
        ("./conffiles", b"/etc/config/fnparse\n", 0o644),
    ]
    control = tar_bytes(ctrl_entries, gz=True)
    out = DIST / ("fnparse_%s_%s.ipk" % (PKG_VERSION, arch["name"]))
    write_ar([
        ("debian-binary", b"2.0\n"),
        ("control.tar.gz", control),
        ("data.tar.gz", data),
    ], out)
    log("生成 %s（%.1f MB）" % (out.name, out.stat().st_size / 1048576))
    return out


def build_apk(arch):
    stage = stage_tree(arch)
    data = data_tar(stage)
    datahash = hashlib.sha256(data).hexdigest()
    pkginfo = """# Generated by openwrt-fnparse build_pkg.py
pkgname = {pkg}
pkgver = {ver}-r{rel}
pkgdesc = {desc}
url = https://github.com/TZYLXS/FnReader-TRE
arch = {arch}
license = MIT
origin = {pkg}
maintainer = {maint}
builddate = {ts}
size = {size}
datahash = {hash}
depend = luci-base
provides = fnparse
""".format(pkg=PKG_NAME, ver=PKG_VERSION, rel=PKG_RELEASE, desc=DESCRIPTION,
           arch=arch["apk_arch"], maint=MAINTAINER, ts=int(time.time()),
           size=dir_size(stage), hash=datahash)

    ctrl_entries = [
        (".PKGINFO", pkginfo.encode(), 0o644),
        (".post-install", POSTINST.encode(), 0o755),
        (".pre-deinstall", PRERM.encode(), 0o755),
        (".post-deinstall", POSTRM.encode(), 0o755),
    ]
    control = tar_bytes(ctrl_entries, gz=True)
    out = DIST / ("fnparse_%s_%s.apk" % (PKG_VERSION, arch["name"]))
    with open(out, "wb") as f:
        f.write(control)
        f.write(data)
    log("生成 %s（%.1f MB）" % (out.name, out.stat().st_size / 1048576))
    return out


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--build", action="store_true", help="先交叉编译 Go 二进制")
    args = ap.parse_args()
    if args.build:
        build_go_binaries()
    for a in ARCHS:
        if not (DIST / "bin" / a["binary"]).exists():
            raise SystemExit("缺少二进制 dist/bin/%s，加 --build 或先编译" % a["binary"])
    DIST.mkdir(parents=True, exist_ok=True)
    outputs = []
    for a in ARCHS:
        outputs.append(build_ipk(a))
        outputs.append(build_apk(a))
    sums = DIST / "SHA256SUMS"
    lines = []
    for p in sorted(outputs):
        lines.append("%s  %s" % (sha256_file(p), p.name))
    sums.write_text("\n".join(lines) + "\n", encoding="utf-8")
    log("校验和写入 %s" % sums.name)
    for line in lines:
        print("  " + line)


if __name__ == "__main__":
    main()
