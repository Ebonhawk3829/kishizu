#!/usr/bin/env python3
"""
Test-bed for the anime release matcher (piece 5).

Goal: given a show definition (canonical name + aliases + expected episode) and a
release title, decide (a) does it belong to this show, (b) which episode is it.

Iterating against the user's hand-labelled reference set in refs.json.
"""
import json, re, sys, unicodedata

# ---------- normalisation ----------

def norm(s: str) -> str:
    """Lowercase, strip accents, collapse non-alphanumerics to single spaces."""
    s = unicodedata.normalize('NFKD', s)
    s = ''.join(c for c in s if not unicodedata.combining(c))
    s = s.lower()
    s = re.sub(r'[^a-z0-9]+', ' ', s)
    return ' '.join(s.split())

# Words that carry no identity signal.
NOISE = {
    'the','a','an','of','to','in','and','or','wa','ga','no','ni','wo','de','kara',
    'season','s','part','ep','episode','ova','ona','tv','movie','special','complete',
    'batch','raw','sub','subs','subbed','dub','dubbed','multi','dual','audio','multi subs',
}

def tokens(s: str) -> set:
    return {t for t in norm(s).split() if t and t not in NOISE}

# ---------- release-name parsing ----------

# [Group] prefix
RE_GROUP = re.compile(r'^\s*\[([^\]]+)\]')
# S01E47 / S1E47 / S03E15
RE_SXE = re.compile(r'\bs(\d{1,2})[ ._-]?e(\d{1,4})\b', re.I)
# " - 47 " or "- 07" style bare number (SubsPlease / Erai-raws)
RE_BARE = re.compile(r'[-–]\s*(\d{1,4})(?:\s|$|[\[(.])')
# "4th Season", "2nd Season", "Season 3"
RE_SEASON_WORD = re.compile(r'\b(\d{1,2})(?:st|nd|rd|th)\s+season\b|\bseason\s+(\d{1,2})\b', re.I)
# resolution
RE_RES = re.compile(r'\b(2160p|1080p|720p|480p|4k)\b', re.I)
# codec
RE_CODEC = re.compile(r'\b(av1|x265|hevc|h\s*265|x264|h\s*264|h264)\b', re.I)
# source
RE_SOURCE = re.compile(r'\b(web-dl|webrip|web|bd|bluray|blu-ray|remux|dsnp|cr|amzn)\b', re.I)

def parse(title: str) -> dict:
    g = RE_GROUP.search(title)
    sxe = RE_SXE.search(title)
    bare = RE_BARE.search(title)
    sw = RE_SEASON_WORD.search(title)
    season_word = None
    if sw:
        season_word = int(sw.group(1) or sw.group(2))
    return {
        'title': title,
        'group': g.group(1).strip() if g else None,
        'sxe_season': int(sxe.group(1)) if sxe else None,
        'sxe_ep': int(sxe.group(2)) if sxe else None,
        'bare': int(bare.group(1)) if bare else None,
        'season_word': season_word,
        'res': (RE_RES.search(title).group(1).lower() if RE_RES.search(title) else None),
        'codec': (RE_CODEC.search(title).group(1).lower().replace(' ', '') if RE_CODEC.search(title) else None),
        'source': (RE_SOURCE.search(title).group(1).lower() if RE_SOURCE.search(title) else None),
    }

# ---------- matching ----------

def title_score(show_aliases, release_title: str) -> float:
    """Best Jaccard-ish overlap between any alias and the release title."""
    rt = tokens(release_title)
    best = 0.0
    for alias in show_aliases:
        at = tokens(alias)
        if not at or not rt:
            continue
        inter = len(at & rt)
        # recall matters more than precision: the alias should be *contained* in the title
        score = inter / len(at)
        best = max(best, score)
    return best

def match(show, release_title: str, threshold=0.6):
    """
    show = {'name':..., 'aliases':[...], 'offset':int, 'group_offsets':{group:int}}

    KEY FINDING: the absolute->local offset is NOT a single number per show. Within one
    show, groups disagree about numbering. BLEACH TYBW ep 7:
        Erai-raws  -> "07"  (local)
        SubsPlease -> "47"  (absolute)
        ToonsHub   -> "S01E47" (absolute)
        VARYG      -> "S01E47" (absolute)
    So offset must be per-group, defaulting to the show-level offset.
    """
    p = parse(release_title)
    sc = title_score(show['aliases'], release_title)
    if sc < threshold:
        return False, None, f'title score {sc:.2f} < {threshold}'

    # Per-group offset wins over the show default.
    offset = show.get('offset', 0)
    if p['group']:
        g = norm(p['group'])
        for gk, goff in show.get('group_offsets', {}).items():
            if norm(gk) in g or g in norm(gk):
                offset = goff
                break

    if p['sxe_ep'] is not None:
        ep = p['sxe_ep'] - offset
        return True, ep, f'SxE {p["sxe_ep"]} - off {offset} -> {ep}'

    if p['bare'] is not None:
        ep = p['bare'] - offset
        return True, ep, f'bare {p["bare"]} - off {offset} -> {ep}'

    return True, None, f'matched but no episode number (score {sc:.2f})'

# ---------- test-bed ----------

SHOWS = {
    'bleach': {
        'name': 'BLEACH: Thousand-Year Blood War - The Calamity',
        'aliases': [
            'BLEACH: Thousand-Year Blood War - The Calamity',
            'Bleach: Sennen Kessen Hen - Kashin Tan',
            'BLEACH Sennen Kessen-hen',
            'Bleach Sennen Kessen Hen',
            'BLEACH Thousand Year Blood War',
        ],
        'offset': 40,   # default: absolute -> local (47 -> 7)
        'group_offsets': {
            'Erai-raws': 0,   # Erai-raws numbers locally already
        },
    },
    'rezero': {
        'name': 'Re:ZERO -Starting Life in Another World- Season 4',
        'aliases': [
            'Re:ZERO -Starting Life in Another World- Season 4',
            'Re:Zero kara Hajimeru Isekai Seikatsu 4th Season',
            'Re ZERO Starting Life in Another World',
            'Re:Zero kara Hajimeru Isekai Seikatsu',
        ],
        'offset': 0,
    },
    'trk': {
        'name': 'Tomb Raider King',
        'aliases': [
            'Tomb Raider King',
            'Dogul Wang',
        ],
        'offset': 0,
    },
}

EXPECTED = {
    '2156727': ('bleach', 7), '2156721': ('bleach', 7), '2156732': ('bleach', 7),
    '2156731': ('bleach', 7), '2156708': ('bleach', 7),
    '2155243': ('rezero', 15), '1949681': ('rezero', 15), '1955094': ('rezero', 15),
    '2155442': ('trk', 9), '2155449': ('trk', 9), '2155440': ('trk', 9),
}

def main():
    refs = json.load(open('refs.json'))
    ok = fail = 0
    for vid, (show_key, want_ep) in sorted(EXPECTED.items()):
        title = refs[vid]['title']
        show = SHOWS[show_key]
        m, ep, why = match(show, title)
        good = m and ep == want_ep
        ok, fail = (ok + 1, fail) if good else (ok, fail + 1)
        flag = 'PASS' if good else 'FAIL'
        print(f"[{flag}] {vid} {show_key:7s} want ep{want_ep:<3} got {str(ep):<5} {why}")
        if not good:
            print(f"        {title[:100]}")
    print(f"\n{ok} passed, {fail} failed")

if __name__ == '__main__':
    main()
