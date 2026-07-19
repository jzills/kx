# -*- mode: python ; coding: utf-8 -*-
# PyInstaller spec for standalone kx builds (onedir). Build with: pyinstaller kx.spec
from PyInstaller.utils.hooks import copy_metadata

a = Analysis(
    ["scripts/pyinstaller_entry.py"],
    pathex=["src"],
    binaries=[],
    # _kx_version() resolves the version via importlib.metadata, so the
    # kx-cli dist-info must ship inside the bundle.
    datas=copy_metadata("kx-cli"),
    hiddenimports=[],
    hookspath=[],
    runtime_hooks=[],
    excludes=[],
    noarchive=False,
)
pyz = PYZ(a.pure)

exe = EXE(
    pyz,
    a.scripts,
    [],
    exclude_binaries=True,
    name="kx",
    debug=False,
    bootloader_ignore_signals=False,
    strip=False,
    upx=False,
    console=True,
)
coll = COLLECT(
    exe,
    a.binaries,
    a.datas,
    strip=False,
    upx=False,
    name="kx",
)
