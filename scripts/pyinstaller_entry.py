"""Entry script for PyInstaller builds; mirrors the `kx` console script."""

from kx.main import app

if __name__ == "__main__":
    app()
