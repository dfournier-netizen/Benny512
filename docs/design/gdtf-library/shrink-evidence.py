"""Re-save evidence/*.png as 256-colour palette PNGs so the screenshots stay
small in the repo. Run after check.mjs:  python3 -I shrink-evidence.py"""
import pathlib
from PIL import Image

ev = pathlib.Path(__file__).resolve().parent / "evidence"
for p in sorted(ev.glob("*.png")):
    before = p.stat().st_size
    im = Image.open(p).convert("RGB")
    im.quantize(colors=256, method=Image.Quantize.MEDIANCUT, dither=Image.Dither.NONE).save(p, optimize=True)
    print(f"{p.name}: {before // 1024} KB -> {p.stat().st_size // 1024} KB")
