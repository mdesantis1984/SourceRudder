#!/usr/bin/env python3
"""Build the final SourceRudder campaign artwork from the approved master PNG.

The script deliberately uses only deterministic Pillow operations: cropping,
resizing, selective accent recolouring, solid overlays, typography, and PNG
compression. It never reads the rejected key-art drafts.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import math
import shutil
from pathlib import Path
from typing import Iterable

from PIL import Image, ImageColor, ImageDraw, ImageEnhance, ImageFont, PngImagePlugin


REPO_ROOT = Path(__file__).resolve().parents[1]
SOURCE = REPO_ROOT / "docs/assets/brand/sourcerudder-hero-master.png"
OUT = REPO_ROOT / "docs/assets/brand/campaign"
SOCIAL_PREVIEW = REPO_ROOT / "docs/assets/sourcerudder-social-preview.png"
FONT_REGULAR = Path("/usr/share/fonts/truetype/noto/NotoSans-Regular.ttf")
FONT_BOLD = Path("/usr/share/fonts/truetype/noto/NotoSans-Bold.ttf")

PALETTE = {
    "mist": "#EEF2FF",
    "pale": "#E0E7FF",
    "soft": "#C7D2FE",
    "lavender": "#A5B4FC",
    "light_indigo": "#818CF8",
    "indigo": "#6366F1",
    "strong": "#4F46E5",
    "deep": "#4338CA",
    "royal": "#3730A3",
    "navy": "#312E81",
    "ink_indigo": "#1E1B4B",
    "near_black": "#090A1A",
    "white": "#F8FAFC",
}

DELIVERABLES: list[dict] = []


def font(size: int, bold: bool = False) -> ImageFont.FreeTypeFont:
    return ImageFont.truetype(str(FONT_BOLD if bold else FONT_REGULAR), size=size)


def recolor_accents(im: Image.Image) -> Image.Image:
    """Map cyan/teal source pixels to indigo while preserving tonal detail."""
    rgb = im.convert("RGB")
    hsv = rgb.convert("HSV")
    h, s, v = hsv.split()
    # Cyan and teal occupy approximately 115..145 in Pillow's 0..255 hue range.
    hue_mask = h.point(lambda x: 255 if 112 <= x <= 148 else 0)
    sat_mask = s.point(lambda x: 255 if x >= 70 else 0)
    mask = Image.composite(hue_mask, Image.new("L", rgb.size, 0), sat_mask)
    target_hue = Image.new("L", rgb.size, 174)  # approved blue-indigo direction
    h = Image.composite(target_hue, h, mask)
    recolored = Image.merge("HSV", (h, s, v)).convert("RGB")
    return recolored


def cover_crop(im: Image.Image, size: tuple[int, int], box: tuple[int, int, int, int] | None = None) -> Image.Image:
    src = im.crop(box) if box else im
    scale = max(size[0] / src.width, size[1] / src.height)
    resized = src.resize((math.ceil(src.width * scale), math.ceil(src.height * scale)), Image.Resampling.LANCZOS)
    left = (resized.width - size[0]) // 2
    top = (resized.height - size[1]) // 2
    return resized.crop((left, top, left + size[0], top + size[1]))


def overlay(im: Image.Image, box: tuple[int, int, int, int], color: str, alpha: int = 255, radius: int = 0) -> None:
    layer = Image.new("RGBA", im.size, (0, 0, 0, 0))
    ImageDraw.Draw(layer).rounded_rectangle(box, radius=radius, fill=(*ImageColor.getrgb(color), alpha))
    im.alpha_composite(layer)


def wrap_text(draw: ImageDraw.ImageDraw, text: str, fnt: ImageFont.FreeTypeFont, max_width: int) -> list[str]:
    """Greedy word wrap that never splits a word (especially SourceRudder)."""
    words = text.split()
    lines: list[str] = []
    current = ""
    for word in words:
        candidate = word if not current else f"{current} {word}"
        if draw.textbbox((0, 0), candidate, font=fnt)[2] <= max_width:
            current = candidate
        else:
            if current:
                lines.append(current)
            current = word
    if current:
        lines.append(current)
    return lines


def draw_wrapped(
    draw: ImageDraw.ImageDraw,
    xy: tuple[int, int],
    text: str,
    fnt: ImageFont.FreeTypeFont,
    fill: str,
    max_width: int,
    spacing: int = 12,
    anchor: str | None = None,
) -> int:
    lines = wrap_text(draw, text, fnt, max_width)
    x, y = xy
    for line in lines:
        draw.text((x, y), line, font=fnt, fill=fill, anchor=anchor)
        bbox = draw.textbbox((x, y), line, font=fnt, anchor=anchor)
        y += bbox[3] - bbox[1] + spacing
    return y


def add_rule(draw: ImageDraw.ImageDraw, xy: tuple[int, int], width: int, color: str = PALETTE["indigo"], thickness: int = 8) -> None:
    x, y = xy
    draw.rounded_rectangle((x, y, x + width, y + thickness), radius=thickness // 2, fill=color)


def add_cta(draw: ImageDraw.ImageDraw, xy: tuple[int, int], text: str, size: int, pad_x: int = 28, pad_y: int = 14) -> tuple[int, int, int, int]:
    fnt = font(size, True)
    x, y = xy
    bbox = draw.textbbox((0, 0), text, font=fnt)
    w, h = bbox[2] - bbox[0], bbox[3] - bbox[1]
    rect = (x, y, x + w + pad_x * 2, y + h + pad_y * 2)
    draw.rounded_rectangle(rect, radius=(h + pad_y * 2) // 2, fill=PALETTE["strong"], outline=PALETTE["lavender"], width=3)
    draw.text((x + pad_x, y + pad_y - bbox[1]), text, font=fnt, fill=PALETTE["white"])
    return rect


def png_metadata() -> PngImagePlugin.PngInfo:
    info = PngImagePlugin.PngInfo()
    info.add_text("ColorSpace", "sRGB")
    info.add_text("Software", "Pillow; build-campaign-assets.py")
    return info


def save_limited(im: Image.Image, path: Path, limit: int | None = None) -> None:
    """Save as RGB/RGBA PNG and progressively reduce color entropy if needed."""
    mode = "RGBA" if im.mode == "RGBA" and im.getextrema()[3] != (255, 255) else "RGB"
    candidate = im.convert(mode)
    path.parent.mkdir(parents=True, exist_ok=True)
    candidate.save(path, "PNG", optimize=True, compress_level=9, pnginfo=png_metadata())
    if limit and path.stat().st_size >= limit:
        alpha = candidate.getchannel("A") if mode == "RGBA" else None
        rgb = candidate.convert("RGB")
        for colors in (192, 128, 96, 92, 80, 64, 48, 32):
            reduced = rgb.quantize(colors=colors, method=Image.Quantize.MEDIANCUT, dither=Image.Dither.NONE).convert("RGB")
            if alpha is not None:
                reduced = reduced.convert("RGBA")
                reduced.putalpha(alpha)
            reduced.save(path, "PNG", optimize=True, compress_level=9, pnginfo=png_metadata())
            if path.stat().st_size < limit:
                break
    if limit and path.stat().st_size >= limit:
        raise RuntimeError(f"{path.name} exceeds {limit} bytes")


def register(path: Path, size: tuple[int, int], purpose: str, copy: Iterable[str], limit: int | None = None) -> None:
    with Image.open(path) as image:
        image.verify()
    with Image.open(path) as checked:
        if checked.size != size:
            raise RuntimeError(f"wrong dimensions for {path.name}: {checked.size}")
        allowed = {"RGB", "RGBA"}
        if checked.mode not in allowed:
            raise RuntimeError(f"wrong color mode for {path.name}: {checked.mode}")
    if limit and path.stat().st_size >= limit:
        raise RuntimeError(f"{path.name} violates size limit")
    DELIVERABLES.append(
        {
            "filename": path.name,
            "dimensions": {"width": size[0], "height": size[1]},
            "purpose": purpose,
            "exact_copy": list(copy),
            "sha256": hashlib.sha256(path.read_bytes()).hexdigest(),
        }
    )


def github_preview(base: Image.Image) -> None:
    size = (1280, 640)
    im = cover_crop(base, size).convert("RGBA")
    # Opaque replacement guarantees that none of the master's English copy survives.
    overlay(im, (545, 0, 1280, 640), PALETTE["near_black"], 255)
    overlay(im, (560, 30, 1250, 610), PALETTE["ink_indigo"], 110, 30)
    d = ImageDraw.Draw(im)
    d.text((610, 82), "SourceRudder", font=font(62, True), fill=PALETTE["white"])
    add_rule(d, (610, 165), 165)
    y = draw_wrapped(d, (610, 198), "Dirige cada búsqueda. Ve cada fuente.", font(34, True), PALETTE["mist"], 590, 10)
    y += 20
    d.text((610, y), "28 herramientas MCP · 16 búsquedas especializadas", font=font(20), fill=PALETTE["soft"])
    add_cta(d, (610, 432), "Conocé SourceRudder en GitHub", 20)
    d.text((610, 535), "github.com/mdesantis1984/SourceRudder", font=font(18), fill=PALETTE["lavender"])
    path = OUT / "sourcerudder-github-preview.png"
    save_limited(im, path, 1_000_000)
    register(path, size, "GitHub social preview", ["SourceRudder", "Dirige cada búsqueda. Ve cada fuente.", "28 herramientas MCP · 16 búsquedas especializadas", "Conocé SourceRudder en GitHub", "github.com/mdesantis1984/SourceRudder"], 1_000_000)


def repository_hero(base: Image.Image) -> None:
    size = (1774, 887)
    if base.size != size:
        raise RuntimeError(f"wrong source dimensions for repository hero: {base.size}")
    path = OUT / "sourcerudder-hero-indigo.png"
    save_limited(base, path, 5_000_000)
    register(
        path,
        size,
        "README hero",
        [
            "SourceRudder",
            "Steer every search. See every source.",
            "Self-hosted research infrastructure for AI agents",
            "28 MCP tools",
            "16 search surfaces",
            "Explicit outcomes",
            "Web",
            "Papers",
            "Datasets",
            "Code",
            "News",
            "Video",
            "More",
        ],
        5_000_000,
    )


def linkedin_personal(base: Image.Image) -> None:
    size = (1200, 1500)
    im = cover_crop(base, size, (0, 0, 760, 887)).convert("RGBA")
    overlay(im, (0, 0, 1200, 610), PALETTE["near_black"], 225)
    overlay(im, (0, 1160, 1200, 1500), PALETTE["near_black"], 235)
    d = ImageDraw.Draw(im)
    add_rule(d, (78, 72), 190, thickness=12)
    draw_wrapped(d, (78, 118), "Creé SourceRudder para que los agentes investiguen con evidencia, no a ciegas.", font(57, True), PALETTE["white"], 1030, 10)
    d.text((78, 1218), "28 herramientas MCP · 16 búsquedas especializadas · Autoalojado", font=font(25, True), fill=PALETTE["soft"])
    add_cta(d, (78, 1292), "Conocé SourceRudder en GitHub", 24)
    d.text((78, 1418), "github.com/mdesantis1984/SourceRudder", font=font(23), fill=PALETTE["lavender"])
    path = OUT / "sourcerudder-linkedin-personal.png"
    save_limited(im, path, 5_000_000)
    register(path, size, "LinkedIn personal post", ["Creé SourceRudder para que los agentes investiguen con evidencia, no a ciegas.", "28 herramientas MCP · 16 búsquedas especializadas · Autoalojado", "Conocé SourceRudder en GitHub", "github.com/mdesantis1984/SourceRudder"], 5_000_000)


def linkedin_company(base: Image.Image) -> None:
    size = (1200, 627)
    im = cover_crop(base, size).convert("RGBA")
    overlay(im, (505, 0, 1200, 627), PALETTE["near_black"], 255)
    d = ImageDraw.Draw(im)
    add_rule(d, (560, 55), 150)
    y = draw_wrapped(d, (560, 92), "Evidencia para tus agentes. No otra respuesta opaca.", font(42, True), PALETTE["white"], 585, 7)
    y += 20
    y = draw_wrapped(d, (560, y), "Gateway de investigación autoalojado y nativo de MCP", font(25), PALETTE["pale"], 570, 5)
    y += 18
    d.text((560, y), "28 herramientas MCP · 16 búsquedas especializadas", font=font(20, True), fill=PALETTE["lavender"])
    add_cta(d, (560, 500), "Conocé SourceRudder en GitHub", 20)
    path = OUT / "sourcerudder-linkedin-empresa.png"
    save_limited(im, path, 5_000_000)
    register(path, size, "LinkedIn company post", ["Evidencia para tus agentes. No otra respuesta opaca.", "Gateway de investigación autoalojado y nativo de MCP", "28 herramientas MCP · 16 búsquedas especializadas", "Conocé SourceRudder en GitHub"], 5_000_000)


def youtube_thumbnail(base: Image.Image) -> None:
    size = (3840, 2160)
    im = cover_crop(base, size).convert("RGBA")
    overlay(im, (1700, 0, 3840, 2160), PALETTE["near_black"], 255)
    d = ImageDraw.Draw(im)
    add_rule(d, (1940, 225), 420, thickness=24)
    d.text((1940, 320), "TU AGENTE", font=font(210, True), fill=PALETTE["white"])
    d.text((1940, 555), "NECESITA FUENTES", font=font(168, True), fill=PALETTE["light_indigo"])
    d.text((1940, 900), "SourceRudder", font=font(190, True), fill=PALETTE["white"])
    d.text((1940, 1200), "28 herramientas MCP", font=font(92, True), fill=PALETTE["soft"])
    add_cta(d, (1940, 1490), "EN GITHUB", 92, 70, 30)
    # The entire lower-right corner remains intentionally empty for the duration badge.
    path = OUT / "sourcerudder-youtube-miniatura.png"
    save_limited(im, path, 2_000_000)
    register(path, size, "YouTube thumbnail", ["TU AGENTE NECESITA FUENTES", "SourceRudder", "28 herramientas MCP", "EN GITHUB"], 2_000_000)


def slide_background(base: Image.Image, index: int) -> Image.Image:
    boxes = {
        1: (0, 0, 760, 887),
        2: (0, 0, 690, 887),
        3: (80, 60, 780, 887),
        4: (0, 30, 760, 887),
        5: (0, 210, 880, 887),
        6: (40, 0, 780, 887),
    }
    im = cover_crop(base, (1200, 1500), boxes[index]).convert("RGBA")
    # Alternate emphasis without inventing new artwork.
    if index in {2, 5}:
        im = ImageEnhance.Brightness(im).enhance(0.68)
    overlay(im, (0, 0, 1200, 1500), PALETTE["near_black"], 74)
    return im


def carousel(base: Image.Image) -> None:
    specs = {
        1: ("¿Tu agente puede demostrar de dónde salió su respuesta?", None),
        2: ("Investigar con IA sigue fragmentado", "APIs, formatos y fallos diferentes"),
        3: ("Una puerta de entrada. Muchas fuentes.", None),
        4: ("28 herramientas MCP", "16 búsquedas especializadas"),
        5: ("Fuentes, advertencias, errores y resultados parciales explícitos", None),
        6: ("Controla la investigación. Conserva la evidencia.", "Conocé SourceRudder en GitHub"),
    }
    for index, (main, secondary) in specs.items():
        im = slide_background(base, index)
        d = ImageDraw.Draw(im)
        if index in {1, 2, 3, 5}:
            overlay(im, (54, 870 if index != 2 else 120, 1146, 1435 if index != 2 else 735), PALETTE["near_black"], 224, 34)
            d = ImageDraw.Draw(im)
            y0 = 940 if index != 2 else 205
            maxw = 990
            size_main = 66 if index != 5 else 57
            y = draw_wrapped(d, (105, y0), main, font(size_main, True), PALETTE["white"], maxw, 12)
            if secondary:
                y += 35
                draw_wrapped(d, (105, y), secondary, font(34), PALETTE["soft"], maxw, 8)
        elif index == 4:
            overlay(im, (55, 820, 1145, 1430), PALETTE["near_black"], 226, 34)
            d = ImageDraw.Draw(im)
            d.text((105, 920), main, font=font(82, True), fill=PALETTE["white"])
            add_rule(d, (105, 1048), 260, thickness=14)
            d.text((105, 1115), secondary, font=font(55, True), fill=PALETTE["soft"])
        else:
            overlay(im, (50, 765, 1150, 1460), PALETTE["near_black"], 232, 34)
            d = ImageDraw.Draw(im)
            y = draw_wrapped(d, (100, 840), main, font(62, True), PALETTE["white"], 1000, 12)
            add_cta(d, (100, y + 46), secondary, 28)
            d.text((100, 1370), "github.com/mdesantis1984/SourceRudder", font=font(25), fill=PALETTE["lavender"])
        # Keep the sequence marker above every content panel, including slide 02.
        d.rounded_rectangle((66, 62, 192, 188), radius=34, fill=PALETTE["strong"], outline=PALETTE["lavender"], width=4)
        d.text((129, 124), f"{index:02d}", font=font(42, True), fill=PALETTE["white"], anchor="mm")
        copy = [main] + ([secondary] if secondary else [])
        if index == 6:
            copy.append("github.com/mdesantis1984/SourceRudder")
        path = OUT / f"sourcerudder-linkedin-carrusel-{index:02d}.png"
        save_limited(im, path, 5_000_000)
        register(path, (1200, 1500), f"LinkedIn carousel slide {index:02d}", copy, 5_000_000)


def robot_medallion(base: Image.Image, size: int, transparent_outside: bool = False) -> Image.Image:
    """Circular-safe crop containing only the approved robot head and lens."""
    art = cover_crop(base, (size, size), (185, 95, 690, 600))
    # 15% safety margin: circular art occupies 70% of the square.
    diameter = int(size * 0.70)
    art = art.resize((diameter, diameter), Image.Resampling.LANCZOS)
    canvas = Image.new("RGBA", (size, size), (0, 0, 0, 0) if transparent_outside else (*ImageColor.getrgb(PALETTE["near_black"]), 255))
    mask = Image.new("L", (diameter, diameter), 0)
    ImageDraw.Draw(mask).ellipse((0, 0, diameter - 1, diameter - 1), fill=255)
    x = y = (size - diameter) // 2
    canvas.paste(art, (x, y), mask)
    ring = ImageDraw.Draw(canvas)
    ring.ellipse((x - 3, y - 3, x + diameter + 2, y + diameter + 2), outline=PALETTE["light_indigo"], width=max(5, size // 100))
    return canvas


def icon_master(base: Image.Image) -> None:
    size = (1024, 1024)
    im = robot_medallion(base, 1024)
    path = OUT / "sourcerudder-icono-master.png"
    save_limited(im, path)
    register(path, size, "Square and circular-safe master icon", [])


def horizontal_logo(base: Image.Image) -> None:
    size = (2400, 800)
    im = Image.new("RGBA", size, (0, 0, 0, 0))
    medallion = robot_medallion(base, 720, transparent_outside=True)
    im.alpha_composite(medallion, (40, 40))
    d = ImageDraw.Draw(im)
    d.text((810, 196), "SourceRudder", font=font(178, True), fill=PALETTE["near_black"])
    add_rule(d, (820, 427), 300, thickness=18)
    d.text((820, 490), "Dirige cada búsqueda. Ve cada fuente.", font=font(61), fill=PALETTE["navy"])
    path = OUT / "sourcerudder-logo-horizontal.png"
    save_limited(im, path)
    register(path, size, "Transparent horizontal logo lockup", ["SourceRudder", "Dirige cada búsqueda. Ve cada fuente."])


def contact_sheet() -> None:
    entries = list(DELIVERABLES)
    cell_w, cell_h = 600, 430
    cols = 3
    rows = math.ceil(len(entries) / cols)
    sheet = Image.new("RGB", (cell_w * cols, cell_h * rows), ImageColor.getrgb(PALETTE["near_black"]))
    d = ImageDraw.Draw(sheet)
    for i, entry in enumerate(entries):
        path = OUT / entry["filename"]
        with Image.open(path) as source:
            has_transparency = source.mode == "RGBA" and source.getextrema()[3] != (255, 255)
            thumb = source.convert("RGBA")
            thumb.thumbnail((cell_w - 40, cell_h - 76), Image.Resampling.LANCZOS)
            preview_color = PALETTE["mist"] if has_transparency else PALETTE["ink_indigo"]
            bg = Image.new("RGBA", thumb.size, ImageColor.getrgb(preview_color) + (255,))
            if thumb.mode == "RGBA":
                bg.alpha_composite(thumb)
                thumb = bg
            x = i % cols * cell_w + (cell_w - thumb.width) // 2
            y = i // cols * cell_h + 18
            sheet.paste(thumb.convert("RGB"), (x, y))
        label_y = i // cols * cell_h + cell_h - 48
        d.text((i % cols * cell_w + 20, label_y), entry["filename"].replace("sourcerudder-", ""), font=font(20, True), fill=PALETTE["mist"])
    save_limited(sheet, OUT / "contact-sheet.png")


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Build SourceRudder campaign assets from the approved hero master.")
    parser.add_argument("--source", type=Path, default=SOURCE)
    parser.add_argument("--output", type=Path, default=OUT)
    parser.add_argument("--social-preview", type=Path, default=SOCIAL_PREVIEW)
    return parser.parse_args()


def main() -> None:
    global SOURCE, OUT
    args = parse_args()
    SOURCE = args.source.resolve()
    OUT = args.output.resolve()
    if not SOURCE.exists():
        raise SystemExit(f"Missing mandatory base artwork: {SOURCE}")
    if not FONT_REGULAR.exists() or not FONT_BOLD.exists():
        raise SystemExit("Noto Sans Regular/Bold are required")
    OUT.mkdir(parents=True, exist_ok=True)
    DELIVERABLES.clear()
    with Image.open(SOURCE) as source:
        base = recolor_accents(source.convert("RGB"))
    repository_hero(base)
    github_preview(base)
    linkedin_personal(base)
    linkedin_company(base)
    youtube_thumbnail(base)
    carousel(base)
    icon_master(base)
    horizontal_logo(base)
    manifest = {
        "source_artwork": SOURCE.name,
        "source_sha256": hashlib.sha256(SOURCE.read_bytes()).hexdigest(),
        "color_space": "sRGB",
        "deliverables": DELIVERABLES,
    }
    (OUT / "manifest.json").write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    contact_sheet()
    social_preview = args.social_preview.resolve()
    social_preview.parent.mkdir(parents=True, exist_ok=True)
    shutil.copyfile(OUT / "sourcerudder-github-preview.png", social_preview)
    print(f"Built and validated {len(DELIVERABLES)} deliverables in {OUT}")


if __name__ == "__main__":
    main()
