#!/usr/bin/env python3
"""
Generate internal/coop/class/syncmask_gen.go, the story-bit SyncMask of
docs/08-coop-design.md §6.5, from the zeldaret/tww decomp:

    python3 scripts/gen_event_masks.py <tww-path>          # write the Go file
    python3 scripts/gen_event_masks.py --check <tww-path>  # exit 1 if it is stale

The SyncMask is every named event bit of bytes 0x00-0x41
(include/d/d_save_event_flag.inc) minus three lists, each derived here with
its citations:

  - D8, the game-cleared bits: every bit a dComIfGs_offEventBit call clears
    in the GZLE01 build. The calls are found in all of src/ and include/
    except d_save.cpp (which only defines offEventBit). A call is left out
    of GZLE01 only when its translation unit is not in GZLE01's splits, or
    a version guard (#if VERSION ..., VERSION = 2 for GZLE01) removes it
    from a unit the decomp matches byte for byte for GZLE01; a guard the
    script can't evaluate, or one in a unit that doesn't match, keeps the
    call (fail closed: a bit wrongly kept out of the SyncMask only stays
    local, a bit wrongly let in stays in the grow-only world for good).
  - LocalOnly: the D8 bits plus the bits §6.5 adds by hand because they are
    per player although nothing clears them (LOCAL_EXTRA below, each with
    the decomp lines that say why; every cited line is checked to still
    say it).
  - Refight: the bits dMeter_recollect_boss_data sets when it takes the
    Ganon's Tower refight snapshot, which dComIfGs_setPlayerRecollectionData
    reads back (d_meter.cpp, d_com_inf_game.cpp).

Every call the script finds but cannot resolve to a named bit, and every
cited line that no longer says what it should, is an error: the script
exits 1 without writing. The report on stderr compares the lists with the
design's (§6.5) and with ww-online's LocalOnly list (docs/event-flags.md,
MIT; numbers only, as cross-check data).

Standard library only.
"""

import argparse
import os
import re
import subprocess
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.dirname(HERE)
OUT = os.path.join(ROOT, "internal", "coop", "class", "syncmask_gen.go")

# GZLE01 is VERSIONS index 2 in configure.py; VERSION_USA in
# include/global.h:63-66.
VERSION = 2
MACROS = {"VERSION": VERSION, "VERSION_DEMO": 0, "VERSION_JPN": 1, "VERSION_USA": 2, "VERSION_PAL": 3}

EVENT_INC = "include/d/d_save_event_flag.inc"
BIT_BYTES = 0x42  # bytes 0x00-0x41 hold the named bits
REG_FIRST = 0x79  # bytes 0x79-0xFF hold the registers
NAMED_BITS = 487
NAMED_REGS = 131
EXCLUDED_FILES = {"src/d/d_save.cpp"}  # defines offEventBit itself

# LocalOnly bits nothing clears (§6.5), with the decomp lines that make
# them per player: (id, why, [(path, line, token the line must hold)]).
LOCAL_EXTRA = [
    (0x0A02, "freezes this player's clock at night until it owns Nayru's Pearl symbol "
             "(dKy_checkEventNightStop), which also picks the night layers (getLayerNo)",
     [("src/d/d_kankyo.cpp", 3162, "ENDLESS_NIGHT"),
      ("src/d/d_kankyo.cpp", 479, "dComIfGs_setTime(0.0f)"),
      ("src/d/d_com_inf_game.cpp", 189, "dKy_checkEventNightStop")]),
    (0x2A08, "until it is set, this player's ship is put at the dock spawn and Link is sent "
             "to the dock point; set, the file resumes at its own return place",
     [("src/d/d_stage.cpp", 1933, "RODE_KORL"),
      ("src/d/actor/d_a_player_main.cpp", 10853, "RODE_KORL"),
      ("src/d/d_com_inf_game.cpp", 1305, "RODE_KORL")]),
    (0x3820, "the Hyrule statue recomputes it from the story every time it is created",
     [("src/d/actor/d_a_obj_YLzou.cpp", 143, "offEventBit(dSv_event_flag_c::MOVED_HYRULE_STATUE)"),
      ("src/d/actor/d_a_obj_YLzou.cpp", 145, "onEventBit(dSv_event_flag_c::MOVED_HYRULE_STATUE)")]),
    (0x3B20, "set with the door password it rolls into register 0xBA0F, which is per player",
     [("src/d/actor/d_a_tag_event.cpp", 112, "onEventBit(dSv_event_flag_c::UNK_3B20)"),
      ("src/d/actor/d_a_tag_event.cpp", 114, "setEventReg(dSv_event_flag_c::UNK_BA0F"),
      ("src/d/actor/d_a_knob00.cpp", 610, "onEventBit(dSv_event_flag_c::UNK_3B20)")]),
]

# Notes on calls the GZLE01 build compiles but normal play never reaches:
# {(path, line): (token the line must hold, note)}. They stay clears.
SITE_NOTES = {
    ("src/d/d_s_menu.cpp", 244): ("offEventBit(dSv_event_flag_c::UNK_2D01)",
                                  "the map-select debug scene's toggle; §6.5 keeps the bit out all the same"),
}

# The design's lists (§6.5), for the report only.
DESIGN_SITES = (50, 10)
DESIGN_D8 = {
    0x0E08, 0x1108, 0x1110, 0x1140, 0x1301, 0x1302, 0x1304, 0x2002, 0x2004, 0x2080, 0x2680,
    0x2801, 0x2802, 0x2804, 0x2940, 0x2980, 0x2D01, 0x2F01, 0x3080, 0x3410, 0x3820, 0x3B01,
    0x3C02, 0x3C04, 0x3C08, 0x3C10, 0x3C20, 0x3C40, 0x3C80, 0x3F01, 0x4008, 0x4020, 0x4040,
    0x4080,
}
DESIGN_REFIGHT = {0x3D80, 0x3D40, 0x3D20, 0x3D10}

# ww-online's 37-bit LocalOnly list (docs/event-flags.md "LocalOnly (37)",
# MIT), as numbers for the cross-check.
WW_ONLINE_LOCAL_ONLY = {
    0x1304, 0x1302, 0x1301, 0x2680,
    0x2080, 0x2004, 0x2002, 0x2804, 0x2802, 0x2801, 0x2980, 0x2940, 0x3B01,
    0x3C80, 0x3C40, 0x3C20, 0x3C10, 0x3C08, 0x3C04, 0x3C02,
    0x2F01, 0x3080, 0x3F01, 0x4080, 0x4040, 0x4008, 0x4020, 0x3410, 0x1140,
    0x3820, 0x0A02, 0x2A08, 0x3B20, 0x3D80, 0x3D40, 0x3D20, 0x3D10,
}


class GenError(Exception):
    pass


def read(tww, rel):
    with open(os.path.join(tww, rel), encoding="utf-8", errors="replace") as f:
        return f.read()


def strip_comments(text):
    """Blank out // and /* */ comments, keeping every newline and offset."""
    out = list(text)
    i, n = 0, len(text)
    while i < n:
        c = text[i]
        if c in "\"'":
            j = i + 1
            while j < n and text[j] != c and text[j] != "\n":
                j += 2 if text[j] == "\\" else 1
            i = j + 1
        elif text.startswith("//", i):
            j = text.find("\n", i)
            j = n if j < 0 else j
            for k in range(i, j):
                out[k] = " "
            i = j
        elif text.startswith("/*", i):
            j = text.find("*/", i + 2)
            j = n if j < 0 else j + 2
            for k in range(i, j):
                if out[k] != "\n":
                    out[k] = " "
            i = j
        else:
            i += 1
    return "".join(out)


def line_of(text, pos):
    return text.count("\n", 0, pos) + 1


# ---------------------------------------------------------------- enum

ENUM_LINE = re.compile(r"^\s*/\*\s*0x[0-9A-Fa-f]+\s*\*/\s*(\w+)\s*=\s*(0x[0-9A-Fa-f]+)\s*,")


def parse_enum(tww):
    """Return ({name: (id, line)} for bits, {name: (id, line)} for registers)."""
    bits, regs, section = {}, {}, None
    for no, line in enumerate(read(tww, EVENT_INC).splitlines(), 1):
        if "// Event bits." in line:
            section = bits
        elif "// Event registers." in line:
            section = regs
        m = ENUM_LINE.match(line)
        if m and section is not None:
            name, val = m.group(1), int(m.group(2), 16)
            if name in bits or name in regs:
                raise GenError(f"{EVENT_INC}:{no}: {name} defined twice")
            section[name] = (val, no)
    if len(bits) != NAMED_BITS or len(regs) != NAMED_REGS:
        raise GenError(f"{EVENT_INC}: {len(bits)} bits and {len(regs)} registers, "
                       f"want {NAMED_BITS} and {NAMED_REGS}")
    ids = set()
    for name, (val, no) in bits.items():
        b, m = val >> 8, val & 0xFF
        if b >= BIT_BYTES or m == 0 or m & (m - 1):
            raise GenError(f"{EVENT_INC}:{no}: bit {name} = 0x{val:04X} is not one bit of bytes 0x00-0x41")
        if val in ids:
            raise GenError(f"{EVENT_INC}:{no}: bit 0x{val:04X} named twice")
        ids.add(val)
    for name, (val, no) in regs.items():
        if val >> 8 < REG_FIRST or val & 0xFF == 0:
            raise GenError(f"{EVENT_INC}:{no}: register {name} = 0x{val:04X} is outside bytes 0x79-0xFF")
    return bits, regs


# ---------------------------------------------------------------- #if

TOKEN = re.compile(r"\s*(?:(0[xX][0-9a-fA-F]+|\d+)[uUlL]*|([A-Za-z_]\w*)|(&&|\|\||==|!=|<=|>=|[<>!()]))")


class Cond:
    """A three-valued #if evaluator: 0, 1 (an int), or None when it depends
    on something other than the version macros."""

    def __init__(self, expr):
        self.toks, pos = [], 0
        expr = expr.strip()
        while pos < len(expr):
            m = TOKEN.match(expr, pos)
            if not m or m.end() == pos:
                self.toks = None
                return
            self.toks.append(m.group(1) or m.group(2) or m.group(3))
            pos = m.end()
        self.i = 0

    def value(self):
        if self.toks is None:
            return None
        try:
            v = self.or_()
        except (IndexError, ValueError):
            return None
        return v if self.i == len(self.toks) else None

    def peek(self):
        return self.toks[self.i] if self.i < len(self.toks) else None

    def take(self):
        t = self.toks[self.i]
        self.i += 1
        return t

    def or_(self):
        v = self.and_()
        while self.peek() == "||":
            self.take()
            w = self.and_()
            v = 1 if (v or w) else (None if v is None or w is None else 0)
        return v

    def and_(self):
        v = self.eq()
        while self.peek() == "&&":
            self.take()
            w = self.eq()
            v = 0 if (v == 0 or w == 0) else (None if v is None or w is None else 1)
        return v

    def eq(self):
        v = self.rel()
        while self.peek() in ("==", "!="):
            op, w = self.take(), self.rel()
            v = None if v is None or w is None else int((v == w) == (op == "=="))
        return v

    def rel(self):
        v = self.unary()
        while self.peek() in ("<", ">", "<=", ">="):
            op, w = self.take(), self.unary()
            if v is None or w is None:
                v = None
            else:
                v = int({"<": v < w, ">": v > w, "<=": v <= w, ">=": v >= w}[op])
        return v

    def unary(self):
        if self.peek() == "!":
            self.take()
            v = self.unary()
            return None if v is None else int(not v)
        return self.primary()

    def primary(self):
        t = self.take()
        if t == "(":
            v = self.or_()
            if self.take() != ")":
                raise ValueError(t)
            return v
        if t == "defined":
            paren = self.peek() == "("
            if paren:
                self.take()
            name = self.take()
            if paren and self.take() != ")":
                raise ValueError(t)
            return 1 if name in MACROS else None
        if t[0].isdigit():
            return int(t, 0)
        return MACROS.get(t)


def and3(a, b):
    if a is False or b is False:
        return False
    if a is None or b is None:
        return None
    return True


class Frame:
    """One #if group. taken is True once a branch was certainly taken, None
    while one may have been (it could not be evaluated), False otherwise."""

    def __init__(self, line, text, value):
        self.line, self.end = line, None
        self.text, self.cur, self.taken = text, value, value


def preprocess(text):
    """Return, per line (1-based), the #if branches it is inside, outermost
    first, as (frame, branch value, branch text) as they stood on that line."""
    lines = text.split("\n")
    stack, per_line = [], [[] for _ in range(len(lines) + 2)]
    i = 0
    while i < len(lines):
        raw, no = lines[i], i + 1
        while raw.endswith("\\") and i + 1 < len(lines):
            i += 1
            raw = raw[:-1] + " " + lines[i]
        per_line[no] = [(f, f.cur, f.text) for f in stack]
        m = re.match(r"\s*#\s*(\w+)\s*(.*)", raw)
        if m:
            d, rest = m.group(1), m.group(2).strip()
            if d == "if":
                v = Cond(rest).value()
                stack.append(Frame(no, "#if " + rest, None if v is None else bool(v)))
            elif d in ("ifdef", "ifndef"):
                name = rest.split()[0] if rest else ""
                stack.append(Frame(no, f"#{d} {rest}", (d == "ifdef") if name in MACROS else None))
            elif d == "elif" and stack:
                f = stack[-1]
                v = Cond(rest).value()
                v = None if v is None else bool(v)
                if f.taken is True:
                    f.cur = False
                elif f.taken is None:
                    f.cur = False if v is False else None
                else:
                    f.cur = v
                f.taken = True if f.cur is True else (None if None in (f.taken, f.cur) else False)
                f.text = "#elif " + rest
            elif d == "else" and stack:
                f = stack[-1]
                f.cur = {True: False, None: None, False: True}[f.taken]
                f.taken = True
                f.text = f"#else of {f.text}"
            elif d == "endif" and stack:
                stack.pop().end = no
        i += 1
    return per_line


def guard_state(branches):
    """(state, (frame, text) deciding it) for a line inside branches."""
    state, why = True, None
    for f, cur, text in branches:
        if cur is False:
            return False, (f, text)
        if cur is None:
            state, why = None, (f, text)
    return state, why


# ---------------------------------------------------------------- build

def gzle01_units(tww):
    units = set()
    paths = [os.path.join(tww, "config", "GZLE01", "splits.txt")]
    rels = os.path.join(tww, "config", "GZLE01", "rels")
    if os.path.isdir(rels):
        for r in sorted(os.listdir(rels)):
            paths.append(os.path.join(rels, r, "splits.txt"))
    for p in paths:
        if not os.path.exists(p):
            continue
        with open(p, encoding="utf-8") as f:
            for line in f:
                m = re.match(r"^(\S+\.(?:cpp|c|cp)):\s*$", line)
                if m:
                    units.add(m.group(1))
    if not units:
        raise GenError("config/GZLE01/splits.txt lists no units")
    return units


def unit_status(tww):
    """{unit path under src/: configure.py status expression}."""
    status = {}
    for line in read(tww, "configure.py").splitlines():
        m = re.search(r"\bObject\((.*?),\s*\"([^\"]+\.(?:cpp|c))\"", line)
        if m:
            status[m.group(2)] = m.group(1).strip()
            continue
        m = re.search(r"\bActorRel\((.*),\s*\"(d_a_\w+)\"", line)
        if m:
            status[f"d/actor/{m.group(2)}.cpp"] = m.group(1).strip()
    return status


def matches_gzle01(status):
    if status is None:
        return False
    if status == "Matching":
        return True
    return any("GZLE01" in g for g in re.findall(r"\bMatchingFor\(([^)]*)\)", status))


# ---------------------------------------------------------------- sites

def resolve(arg, bits, where):
    arg = arg.strip()
    m = re.fullmatch(r"dSv_event_flag_c::(\w+)", arg)
    if m:
        if m.group(1) not in bits:
            raise GenError(f"{where}: {arg} is not a named event bit")
        return bits[m.group(1)][0]
    if re.fullmatch(r"0[xX][0-9a-fA-F]+", arg):
        v = int(arg, 16)
        if v not in {b for b, _ in bits.values()}:
            raise GenError(f"{where}: {arg} is not a named event bit")
        return v
    m = re.fullmatch(r"VERSION_SELECT\((.*)\)", arg, re.S)
    if m:
        parts = split_args(m.group(1))
        if len(parts) != 4:
            raise GenError(f"{where}: VERSION_SELECT with {len(parts)} arguments")
        return resolve(parts[VERSION], bits, where)
    raise GenError(f"{where}: cannot resolve offEventBit argument {arg!r}")


def split_args(s):
    parts, depth, cur = [], 0, ""
    for c in s:
        if c == "," and depth == 0:
            parts.append(cur)
            cur = ""
            continue
        depth += c == "("
        depth -= c == ")"
        cur += c
    parts.append(cur)
    return parts


def call_arg(text, pos, where):
    """The text between the parentheses of the call whose name ends at pos."""
    m = re.compile(r"\s*\(").match(text, pos)
    if not m:
        raise GenError(f"{where}: offEventBit not followed by a call")
    depth, i = 1, m.end()
    while i < len(text) and depth:
        depth += text[i] == "("
        depth -= text[i] == ")"
        i += 1
    if depth:
        raise GenError(f"{where}: unbalanced call")
    return text[m.end():i - 1]


def source_files(tww):
    for top in ("src", "include"):
        for dirpath, dirnames, filenames in os.walk(os.path.join(tww, top)):
            dirnames.sort()
            for fn in sorted(filenames):
                if fn.endswith((".cpp", ".c", ".h", ".hpp", ".inc")):
                    rel = os.path.relpath(os.path.join(dirpath, fn), tww).replace(os.sep, "/")
                    if rel not in EXCLUDED_FILES:
                        yield rel


class Site:
    def __init__(self, path, line, bit):
        self.path, self.line, self.bit = path, line, bit
        self.gzle01, self.why = True, ""

    @property
    def at(self):
        return f"{os.path.basename(self.path)}:{self.line}"


def find_sites(tww, bits):
    """Every dComIfGs_offEventBit (or direct getEvent().offEventBit) call."""
    sites, texts = [], {}
    for rel in source_files(tww):
        raw = read(tww, rel)
        if "offEventBit" not in raw:
            continue
        text = strip_comments(raw)
        texts[rel] = text
        for m in re.finditer(r"(\w*offEventBit)\b", text):
            ident, p = m.group(1), m.start()
            where = f"{rel}:{line_of(text, p)}"
            before = text[max(0, p - 64):p]
            if ident == "dComIfGs_offEventBit":
                if re.search(r"\bvoid\s+$", before):
                    continue  # the inline wrapper's definition
                arg = call_arg(text, m.end(), where)
                sites.append(Site(rel, line_of(text, p), resolve(arg, bits, where)))
            elif ident == "offEventBit":
                if re.search(r"\bvoid\s+$", before):
                    continue  # dSv_event_c's declaration (d_save.h)
                if re.search(r"getTmp\(\)\s*\.\s*$", before):
                    continue  # mTmp, not mEvent (dComIfGs_offTmpBit)
                if re.search(r"getEvent\(\)\s*\.\s*$", before):
                    arg = call_arg(text, m.end(), where).strip()
                    prev = text[:p].rstrip().rsplit("\n", 2)[-2:]
                    if re.fullmatch(r"\w+", arg) and any(
                            re.search(rf"void\s+dComIfGs_offEventBit\s*\(\s*u16\s+{arg}\s*\)", l) for l in prev):
                        continue  # the wrapper's body
                    sites.append(Site(rel, line_of(text, p), resolve(arg, bits, where)))
                    continue
                raise GenError(f"{where}: offEventBit used in a form the script does not know")
            else:
                raise GenError(f"{where}: {ident} is not a known offEventBit form")
    return sites, texts


def includers(tww, rel):
    """(.cpp path, line) of every #include of rel under src/."""
    out = []
    want = {rel[len("src/"):] if rel.startswith("src/") else rel}
    for cand in source_files(tww):
        if not cand.endswith((".cpp", ".c")) or not cand.startswith("src/"):
            continue
        raw = read(tww, cand)
        for m in re.finditer(r"^\s*#\s*include\s+\"([^\"]+)\"", raw, re.M):
            inc = m.group(1)
            local = os.path.normpath(os.path.join(os.path.dirname(cand), inc)).replace(os.sep, "/")
            if inc in want or local == rel:
                out.append((cand, line_of(raw, m.start())))
    return out


def describe(decider, path):
    """The guard that decided a line's state, as "#if X (file.cpp:12-30)"."""
    f, text = decider
    return f"{text.strip()} ({os.path.basename(path)}:{f.line}-{f.end})"


def classify_sites(tww, sites, texts, warn):
    units, status = gzle01_units(tww), unit_status(tww)
    branches = {rel: preprocess(t) for rel, t in texts.items()}
    for s in sites:
        state, decider = guard_state(branches[s.path][s.line])
        tus = []  # (translation unit, state, the guard deciding it, its file)
        if s.path.endswith((".cpp", ".c")):
            tus.append((s.path, state, decider, s.path))
        elif s.path.startswith("src/"):
            for inc, line in includers(tww, s.path):
                ist, idec = guard_state(preprocess(strip_comments(read(tww, inc)))[line])
                if state is False or (state is None and ist is not False):
                    tus.append((inc, and3(state, ist), decider, s.path))
                else:
                    tus.append((inc, ist, idec, inc))
            if not tus:
                raise GenError(f"{s.at}: no translation unit includes {s.path}")
        else:
            raise GenError(f"{s.path}:{s.line}: an offEventBit call in a header")
        s.gzle01, whys = False, []
        for tu, st, dec, where in tus:
            unit = tu[len("src/"):]
            if unit not in units:
                whys.append(f"{unit} is not in GZLE01's splits")
            elif st is False and matches_gzle01(status.get(unit)):
                whys.append(f"{describe(dec, where)}; {os.path.basename(unit)} matches GZLE01")
            else:
                s.gzle01 = True
                if st is False:
                    warn(f"{s.at}: guarded out by {describe(dec, where)}, but {unit} does not "
                         f"match GZLE01 ({status.get(unit)}); kept as a clear")
                elif st is None:
                    warn(f"{s.at}: {describe(dec, where)} can't be evaluated; kept as a clear")
        s.why = "; ".join(whys) if not s.gzle01 else ""


def check_registers(tww, bits):
    """setEventReg replaces the bits under its mask (d_save.cpp:1202-1205),
    so a register id in bytes 0x00-0x41 would clear story bits: refuse any
    constant one."""
    for rel in source_files(tww):
        raw = read(tww, rel)
        if "setEventReg" not in raw:
            continue
        text = strip_comments(raw)
        for m in re.finditer(r"\bsetEventReg\s*\(\s*(dSv_event_flag_c::(\w+)|0[xX][0-9a-fA-F]+)\s*,", text):
            where = f"{rel}:{line_of(text, m.start())}"
            if m.group(2) is not None:
                if m.group(2) in bits:
                    raise GenError(f"{where}: setEventReg on event bit {m.group(2)}: "
                                   f"a register write over story bits")
            elif int(m.group(1), 16) >> 8 < REG_FIRST:
                raise GenError(f"{where}: setEventReg on {m.group(1)}, below the registers")


# ---------------------------------------------------------------- refight

def function_body(text, signature, where):
    m = re.search(signature + r"\s*\{", text)
    if not m:
        raise GenError(f"{where}: {signature} not found")
    depth, i = 1, m.end()
    while i < len(text) and depth:
        depth += text[i] == "{"
        depth -= text[i] == "}"
        i += 1
    return m.start(), i


def refight_bits(tww, bits):
    rel = "src/d/d_meter.cpp"
    text = strip_comments(read(tww, rel))
    lo, hi = function_body(text, r"void\s+dMeter_recollect_boss_data\s*\(\s*\)", rel)
    body = text[lo:hi]
    if "dComIfGs_copyPlayerRecollectionData()" not in body:
        raise GenError(f"{rel}: dMeter_recollect_boss_data no longer takes the refight snapshot")
    found = []
    for m in re.finditer(r"dComIfGs_onEventBit\s*\(\s*dSv_event_flag_c::(\w+)\s*\)", body):
        where = f"{rel}:{line_of(text, lo + m.start())}"
        found.append((resolve("dSv_event_flag_c::" + m.group(1), bits, where), line_of(text, lo + m.start())))
    rel2 = "src/d/d_com_inf_game.cpp"
    text2 = strip_comments(read(tww, rel2))
    lo2, hi2 = function_body(text2, r"void\s+dComIfGs_setPlayerRecollectionData\s*\(\s*\)", rel2)
    reads = {}
    for m in re.finditer(r"dComIfGs_isEventBit\s*\(\s*dSv_event_flag_c::(\w+)\s*\)", text2[lo2:hi2]):
        reads.setdefault(bits[m.group(1)][0], line_of(text2, lo2 + m.start()))
    out = []
    for bit, line in found:
        if bit not in reads:
            raise GenError(f"{rel}:{line}: refight bit 0x{bit:04X} is not read by "
                           f"dComIfGs_setPlayerRecollectionData")
        out.append((bit, f"d_meter.cpp:{line}; d_com_inf_game.cpp:{reads[bit]}"))
    if len(out) != 4:
        raise GenError(f"{rel}: {len(out)} refight bits, want 4 (one per dungeon)")
    return out


def check_cites(tww, cites):
    for path, line, token in cites:
        lines = read(tww, path).splitlines()
        if line > len(lines) or token not in lines[line - 1]:
            raise GenError(f"{path}:{line} no longer holds {token!r}: update LOCAL_EXTRA")


# ---------------------------------------------------------------- output

def merge_cites(parts):
    """Join file:line cites, folding lines of one file: a.cpp:1; a.cpp:2 -> a.cpp:1, :2."""
    out, last = [], None
    for c in parts:
        f, _, l = c.rpartition(":")
        if f == last:
            out[-1] += f", :{l}"
        else:
            out.append(c)
            last = f
    return "; ".join(out)


def go_string(s):
    return '"' + s.replace("\\", "\\\\").replace('"', '\\"') + '"'


def generate(tww, warn):
    bits, regs = parse_enum(tww)
    name_of = {v: n for n, (v, _) in bits.items()}
    line_of_bit = {v: l for n, (v, l) in bits.items()}

    sites, texts = find_sites(tww, bits)
    classify_sites(tww, sites, texts, warn)
    sites.sort(key=lambda s: (s.path, s.line))

    cleared, notes = {}, {}
    for (path, line), (token, note) in SITE_NOTES.items():
        check_cites(tww, [(path, line, token)])
    for s in sites:
        if s.gzle01:
            cleared.setdefault(s.bit, []).append(s.at)
        if (s.path, s.line) in SITE_NOTES:
            s.why = SITE_NOTES[(s.path, s.line)][1]
            notes.setdefault(s.bit, []).append(s.why)

    local = {b: list(at) for b, at in cleared.items()}
    extra_why = {}
    for bit, why, cites in LOCAL_EXTRA:
        if bit not in name_of:
            raise GenError(f"LOCAL_EXTRA 0x{bit:04X} is not a named event bit")
        check_cites(tww, cites)
        local.setdefault(bit, [])
        for p, l, _ in cites:
            if f"{os.path.basename(p)}:{l}" not in local[bit]:
                local[bit].append(f"{os.path.basename(p)}:{l}")
        extra_why[bit] = why

    check_registers(tww, bits)
    refight = refight_bits(tww, bits)
    excluded = set(local) | {b for b, _ in refight}

    named = [0] * BIT_BYTES
    for v in name_of:
        named[v >> 8] |= v & 0xFF
    mask = list(named)
    for b in excluded:
        mask[b >> 8] &= ~(b & 0xFF) & 0xFF

    report = Report(sites, cleared, local, refight, mask)

    files = sorted({s.path for s in sites})
    gz_files = sorted({s.path for s in sites if s.gzle01})
    n_gz = sum(s.gzle01 for s in sites)
    rev = decomp_rev(tww)

    o = []
    w = o.append
    w("// Code generated by scripts/gen_event_masks.py from zeldaret/tww"
      + (f" at {rev}" if rev else "") + "; DO NOT EDIT.")
    w("")
    w("package class")
    w("")
    w('import "github.com/StephenSHorton/ww-multiplayer/wwapi/addr"')
    w("")
    w("// syncMask[i] is the story bits of event byte i that sync (§6.5): the")
    w("// named bits (d_save_event_flag.inc) minus localOnlyBits and refightBits")
    w("// (gameClearedBits are all in localOnlyBits). Each line names the bits")
    w("// left out of its byte.")
    w("var syncMask = [addr.EventBitBytes]byte{")
    for i in range(BIT_BYTES):
        gone = [f"0x{(i << 8) | (1 << k):04X}" for k in range(7, -1, -1)
                if (named[i] >> k) & 1 and not (mask[i] >> k) & 1]
        note = f"0x{i:02X}" + (": minus " + " ".join(gone) if gone else "")
        w(f"\t0x{mask[i]:02X}, // {note}")
    w("}")
    w("")
    w(f"// offEventBitSites is every dComIfGs_offEventBit call in the decomp")
    w(f"// outside d_save.cpp: {len(sites)} in {len(files)} files, {n_gz} of them in {len(gz_files)} files")
    w("// the GZLE01 build compiles. A call it doesn't compile says why.")
    w("var offEventBitSites = []EventSite{")
    for s in sites:
        w(f"\t{{ID: 0x{s.bit:04X}, At: {go_string(s.at)}, GZLE01: {'true' if s.gzle01 else 'false'}}},"
          + (f" // {s.why}" if s.why else ""))
    w("}")
    w("")
    w("// gameClearedBits (§6.5's D8) is every bit a dComIfGs_offEventBit call")
    w("// the GZLE01 build compiles clears: OR-merging one would fight the game.")
    w("var gameClearedBits = []EventBit{")
    for b in sorted(cleared):
        w(f"\t// {name_of[b]}, d_save_event_flag.inc:{line_of_bit[b]}"
          + (": " + "; ".join(notes[b]) if b in notes else ""))
        w(f"\t{{ID: 0x{b:04X}, Name: {go_string(name_of[b])}, Cite: {go_string(merge_cites(cleared[b]))}}},")
    w("}")
    w("")
    w("// localOnlyBits (§6.5's LocalOnly) is every game-cleared bit, plus the")
    w("// bits that are each player's own although nothing clears them.")
    w("var localOnlyBits = []EventBit{")
    for b in sorted(local):
        w(f"\t// {name_of[b]}, d_save_event_flag.inc:{line_of_bit[b]}"
          + ": " + ("cleared by the game" if b in cleared else "")
          + (" (" + "; ".join(notes[b]) + ")" if b in notes else "")
          + (", and " if b in cleared and b in extra_why else "") + extra_why.get(b, ""))
        w(f"\t{{ID: 0x{b:04X}, Name: {go_string(name_of[b])}, Cite: {go_string(merge_cites(local[b]))}}},")
    w("}")
    w("")
    w("// refightBits are set as dMeter_recollect_boss_data takes a dungeon's")
    w("// Ganon's Tower refight snapshot, and read back by")
    w("// dComIfGs_setPlayerRecollectionData: a player given one would never")
    w("// take its own snapshot.")
    w("var refightBits = []EventBit{")
    for b, cite in refight:
        w(f"\t// {name_of[b]}, d_save_event_flag.inc:{line_of_bit[b]}")
        w(f"\t{{ID: 0x{b:04X}, Name: {go_string(name_of[b])}, Cite: {go_string(cite)}}},")
    w("}")
    return "\n".join(_wrap_comments(o)) + "\n", report


def _wrap_comments(lines, width=100):
    """Wrap over-long // comment lines (an entry's reason) at word breaks."""
    out = []
    for line in lines:
        stripped = line.lstrip("\t")
        indent = line[:len(line) - len(stripped)]
        if not stripped.startswith("// ") or len(line) <= width:
            out.append(line)
            continue
        words, cur = stripped[3:].split(" "), ""
        for wd in words:
            if cur and len(indent) * 4 + 3 + len(cur) + 1 + len(wd) > width:
                out.append(f"{indent}// {cur}")
                cur = wd
            else:
                cur = f"{cur} {wd}" if cur else wd
        out.append(f"{indent}// {cur}")
    return out


def decomp_rev(tww):
    try:
        r = subprocess.run(["git", "-C", tww, "rev-parse", "--short=8", "HEAD"],
                           capture_output=True, text=True, timeout=10)
    except (OSError, subprocess.SubprocessError):
        return ""
    return r.stdout.strip() if r.returncode == 0 else ""


class Report:
    def __init__(self, sites, cleared, local, refight, mask):
        self.sites, self.cleared, self.local, self.refight, self.mask = sites, cleared, local, refight, mask

    def lines(self):
        hx = lambda s: " ".join(f"0x{b:04X}" for b in sorted(s)) or "none"
        files = {s.path for s in self.sites}
        gz = [s for s in self.sites if s.gzle01]
        out = [
            f"offEventBit calls outside d_save.cpp: {len(self.sites)} in {len(files)} files "
            f"(design: {DESIGN_SITES[0]} in {DESIGN_SITES[1]}); "
            f"{len(gz)} in {len({s.path for s in gz})} files compiled into GZLE01",
        ]
        for s in self.sites:
            if not s.gzle01:
                out.append(f"  not in GZLE01: {s.at} 0x{s.bit:04X} ({s.why})")
        d8 = set(self.cleared)
        out.append(f"D8 (game-cleared in GZLE01): {len(d8)} bits (design: {len(DESIGN_D8)})")
        out.append(f"  design only: {hx(DESIGN_D8 - d8)}")
        out.append(f"  decomp only: {hx(d8 - DESIGN_D8)}")
        rf = {b for b, _ in self.refight}
        out.append(f"refight: {hx(rf)}" + ("" if rf == DESIGN_REFIGHT else f" (design: {hx(DESIGN_REFIGHT)})"))
        lo = set(self.local)
        out.append(f"LocalOnly: {len(lo)} bits; with refight {len(lo | rf)} "
                   f"(ww-online LocalOnly: {len(WW_ONLINE_LOCAL_ONLY)}, refight included)")
        out.append(f"  kept out here, synced by ww-online: {hx((lo | rf) - WW_ONLINE_LOCAL_ONLY)}")
        out.append(f"  kept out by ww-online, synced here: {hx(WW_ONLINE_LOCAL_ONLY - (lo | rf))}")
        out.append(f"SyncMask: {sum(bin(m).count('1') for m in self.mask)} of {NAMED_BITS} named bits")
        return out


def self_test():
    """Check the #if evaluator and the comment stripper on small inputs."""
    for expr, want in [
        ("VERSION == VERSION_DEMO", 0), ("VERSION > VERSION_DEMO", 1), ("VERSION <= VERSION_JPN", 0),
        ("VERSION == VERSION_USA || VERSION == VERSION_PAL", 1), ("!(VERSION > VERSION_JPN)", 0),
        ("0", 0), ("1", 1), ("defined(VERSION)", 1), ("DEBUG", None), ("DEBUG && 0", 0),
        ("DEBUG || 1", 1), ("DEBUG && VERSION", None), ("VERSION ==", None), ("(1", None),
    ]:
        got = Cond(expr).value()
        if got != want:
            raise GenError(f"self-test: #if {expr}: {got}, want {want}")
    text = "\n".join([
        "a",                                  # 1
        "#if VERSION == VERSION_DEMO",        # 2
        "b",                                  # 3
        "#elif VERSION == VERSION_USA",       # 4
        "c",                                  # 5
        "#else",                              # 6
        "d",                                  # 7
        "#endif",                             # 8
        "#ifdef DEBUG",                       # 9
        "e",                                  # 10
        "#else",                              # 11
        "#if 0",                              # 12
        "f",                                  # 13
        "#endif",                             # 14
        "g",                                  # 15
        "#endif",                             # 16
    ])
    lines = preprocess(text)
    for no, want in {1: True, 3: False, 5: True, 7: False, 10: None, 13: False, 15: None}.items():
        got = guard_state(lines[no])[0]
        if got != want:
            raise GenError(f"self-test: line {no}: {got}, want {want}")
    stripped = strip_comments('x // y\n"//" /* z\n */ w')
    if stripped != 'x     \n"//"     \n    w':
        raise GenError(f"self-test: strip_comments gave {stripped!r}")
    print("gen_event_masks: self-test passed", file=sys.stderr)


def shown(path):
    rel = os.path.relpath(path, ROOT)
    return path if rel.startswith("..") else rel


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0].strip())
    ap.add_argument("tww", nargs="?", help="path to a zeldaret/tww checkout")
    ap.add_argument("--check", action="store_true", help="exit 1 if the committed file is stale")
    ap.add_argument("--out", default=OUT, help="the Go file (default: %(default)s)")
    ap.add_argument("-q", "--quiet", action="store_true", help="no report on stderr")
    ap.add_argument("--self-test", action="store_true", help="test the #if evaluator and exit")
    args = ap.parse_args()
    if args.self_test:
        try:
            self_test()
        except GenError as e:
            print(f"gen_event_masks: {e}", file=sys.stderr)
            return 1
        return 0
    if not args.tww:
        ap.error("the tww path is required")

    warnings = []
    try:
        src, report = generate(args.tww, warnings.append)
    except (GenError, OSError) as e:
        print(f"gen_event_masks: {e}", file=sys.stderr)
        return 1
    if not args.quiet:
        for line in report.lines() + [f"warning: {w}" for w in warnings]:
            print(line, file=sys.stderr)

    if args.check:
        try:
            with open(args.out, encoding="utf-8") as f:
                have = f.read()
        except OSError as e:
            print(f"gen_event_masks: {e}", file=sys.stderr)
            return 1
        if have != src:
            print(f"gen_event_masks: {shown(args.out)} is stale: "
                  f"run scripts/gen_event_masks.py {args.tww}", file=sys.stderr)
            return 1
        print(f"gen_event_masks: {shown(args.out)} is up to date", file=sys.stderr)
        return 0

    tmp = args.out + ".tmp"
    with open(tmp, "w", encoding="utf-8", newline="\n") as f:
        f.write(src)
    os.replace(tmp, args.out)
    print(f"gen_event_masks: wrote {shown(args.out)}", file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())
