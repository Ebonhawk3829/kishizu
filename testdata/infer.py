#!/usr/bin/env python3
"""
Can per-group episode offsets be INFERRED from ranked training examples?

The training UX gives us, for one episode, a set of release titles the user would
have been happy with. Since they're all "episode N", true_ep is known for the whole
set — so each example yields (group, raw_number, true_ep) and the offset falls out
as raw - true_ep.

This module derives a full show config from examples alone, then tests whether that
config generalises to groups that were NEVER in the training set.

Run: python3 infer.py
"""
import re, sys, json
from collections import Counter

sys.path.insert(0, '.')
from match import parse, norm, tokens, title_score, RE_SXE, RE_BARE

QUALITY = (r'\b(2160p|1080p|720p|480p|4k|web-?dl|webrip|web|bd|blu-?ray|remux|avc|hevc|'
           r'h\.?264|h\.?265|x264|x265|av1|aac|flac|opus|e-?ac-?3|ac3|ddp|10bit|hi10|'
           r'dsnp|cr|amzn|nf|adn|iqiyi|dual|audio|multi|subs?|dub|dubbed|raw|batch|'
           r'complete|v2|v3|repack|proper|weekly)\b')


def extract_aliases(title):
    """Pull plausible show-title strings out of a release name."""
    s = title
    s = re.sub(r'^\s*\[[^\]]+\]\s*', '', s)          # drop leading [Group]

    # Parentheticals often carry the romaji/official title — capture before removing.
    paren = []
    for pm in re.finditer(r'\(([^)]*)\)', s):
        for part in pm.group(1).split(','):
            part = part.strip()
            if part and not re.search(r'sub|dub|audio|multi|weekly', part, re.I):
                paren.append(part)
    s = re.sub(r'\([^)]*\)', ' ', s)

    # Cut at the episode marker — everything before it is the title.
    m = RE_SXE.search(s)
    if m:
        s = s[:m.start()]
    else:
        m = RE_BARE.search(s)
        if m:
            s = s[:m.start()]

    s = re.sub(r'\[[^\]]*\]', ' ', s)
    s = re.sub(QUALITY, ' ', s, flags=re.I)
    s = re.sub(r'[-–:|]\s*$', ' ', s)
    s = ' '.join(s.split())

    out = []
    if s and len(s) > 2:
        out.append(s)
    out.extend(paren)
    return out


def infer_show(canonical, examples):
    """
    examples: list of (release_title, true_episode)
    Returns a show config derived purely from those examples.
    """
    aliases = {canonical}
    per_group = {}          # group -> Counter{offset: count}
    all_offsets = Counter()

    for title, true_ep in examples:
        p = parse(title)
        for a in extract_aliases(title):
            aliases.add(a)

        raw = p['sxe_ep'] if p['sxe_ep'] is not None else p['bare']
        if raw is None:
            continue
        off = raw - true_ep
        g = p['group'] or '(none)'
        per_group.setdefault(g, Counter())[off] += 1
        all_offsets[off] += 1

    resolved = {g: c.most_common(1)[0][0] for g, c in per_group.items()}
    return {
        'name': canonical,
        'aliases': sorted(aliases),
        'group_offsets': resolved,
        'offsets_seen': sorted(all_offsets),
        'default_offset': all_offsets.most_common(1)[0][0] if all_offsets else 0,
    }


def match_inferred(show, title, expected_ep=None, max_ep=None):
    """Match using an inferred config, including groups never seen in training."""
    p = parse(title)
    sc = title_score(show['aliases'], title)
    if sc < 0.6:
        return None, f'no title match ({sc:.2f})'

    raw = p['sxe_ep'] if p['sxe_ep'] is not None else p['bare']
    if raw is None:
        return None, 'no episode number'

    g = p['group'] or '(none)'
    known = show['group_offsets'].get(g)
    if known is not None:
        return raw - known, f'group "{g}" known offset {known}'

    # Unseen group: try every offset this show has exhibited, keep plausible results.
    cands = []
    for off in show['offsets_seen']:
        ep = raw - off
        if ep < 1:
            continue
        if max_ep and ep > max_ep:
            continue
        cands.append((ep, off))
    if not cands:
        return None, f'unseen group "{g}", no plausible offset'

    if expected_ep is not None:
        cands.sort(key=lambda c: abs(c[0] - expected_ep))
    else:
        cands.sort(key=lambda c: c[0])
    ep, off = cands[0]
    return ep, f'group "{g}" UNSEEN -> inferred offset {off}'


# --------------------------------------------------------------------------

REFS = json.load(open('refs.json'))

BLEACH = 'BLEACH: Thousand-Year Blood War - The Calamity'
REZERO = 'Re:ZERO -Starting Life in Another World- Season 4'
TRK = 'Tomb Raider King'

ALL = {
    BLEACH: [('2156727', 7), ('2156721', 7), ('2156732', 7), ('2156731', 7), ('2156708', 7)],
    REZERO: [('2155243', 15), ('1949681', 15), ('1955094', 15)],
    TRK:    [('2155442', 9), ('2155449', 9), ('2155440', 9)],
}


def run(name, train_ids, test_ids, expected_ep, max_ep=None):
    show = infer_show(name, [(REFS[i]['title'], ep) for i, ep in train_ids])
    print(f"\n--- {name}")
    print(f"    trained on {len(train_ids)} example(s)")
    print(f"    group offsets: {show['group_offsets']}")
    print(f"    offsets seen : {show['offsets_seen']}")
    ok = 0
    for vid, want in test_ids:
        ep, why = match_inferred(show, REFS[vid]['title'], expected_ep, max_ep)
        good = ep == want
        ok += good
        print(f"    [{'PASS' if good else 'FAIL'}] {vid} want {want} got {ep}  ({why})")
    print(f"    => {ok}/{len(test_ids)}")
    return ok, len(test_ids)


def main():
    total_ok = total = 0

    # 1. Train on everything (sanity: inference reproduces the hand-written config)
    for name, ids in ALL.items():
        o, t = run(name, ids, ids, ids[0][1], max_ep=30)
        total_ok += o; total += t

    # 2. THE KEY TEST: train on 2 BLEACH examples, match all 5.
    #    ToonsHub and VARYG are never seen in training.
    o, t = run(BLEACH, [('2156727', 7), ('2156721', 7)], ALL[BLEACH], 7, max_ep=30)
    total_ok += o; total += t

    # 3. Minimal: ONE example per show.
    for name, ids in ALL.items():
        o, t = run(name, ids[:1], ids, ids[0][1], max_ep=30)
        total_ok += o; total += t

    print(f"\n===== {total_ok}/{total} overall")


if __name__ == '__main__':
    main()
