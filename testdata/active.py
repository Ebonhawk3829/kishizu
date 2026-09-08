#!/usr/bin/env python3
"""
Active-learning training loop prototype.

The idea (user's): instead of the user hunting for examples, the tool proposes
candidates and the user just says yes/no. Each round:

  1. User gives ONE seed example ("this is a good release for episode N")
  2. Tool searches Nyaa, ranks candidates by what it currently believes
  3. Tool presents the top few it is LEAST sure about
  4. User says yes/no
  5. Tool updates its model and re-ranks
  6. Repeat until the user says "finished"

This script simulates that loop offline against cached Nyaa search results, so we
can see whether the loop actually converges and how many rounds it takes.

Run: python3 active.py
"""
import re, sys, json, html, glob, os
from collections import Counter

sys.path.insert(0, '.')
from match import parse, norm, tokens, title_score, RE_SXE, RE_BARE
from infer import infer_show, extract_aliases

CACHE = 'search_cache'
REFS = json.load(open('refs.json'))


# ---------- candidate pool ----------

def load_pool(query_file_map):
    """Load cached Nyaa search result pages -> list of (title, url)."""
    pool = []
    for path in sorted(glob.glob(os.path.join(CACHE, '*.html'))):
        s = open(path, encoding='utf-8', errors='replace').read()
        for m in re.finditer(r'href="(/view/(\d+))"[^>]*title="([^"]*)"', s):
            url, vid, title = m.group(1), m.group(2), html.unescape(m.group(3))
            pool.append({'id': vid, 'title': title, 'url': 'https://nyaa.si' + url})
    # dedupe by id
    seen = set()
    out = []
    for r in pool:
        if r['id'] in seen:
            continue
        seen.add(r['id'])
        out.append(r)
    return out


# ---------- belief model ----------

class Model:
    def __init__(self, canonical, seed_title, seed_ep):
        self.canonical = canonical
        self.examples = [(seed_title, seed_ep)]     # accepted
        self.rejected = []                          # rejected titles
        self.aliases = {canonical} | set(extract_aliases(seed_title))
        self.group_offsets = {}
        self.offsets_seen = set()
        self._refit()

    def _refit(self):
        per_group = {}
        all_off = Counter()
        for title, ep in self.examples:
            p = parse(title)
            raw = p['sxe_ep'] if p['sxe_ep'] is not None else p['bare']
            if raw is None:
                continue
            off = raw - ep
            g = p['group'] or '(none)'
            per_group.setdefault(g, Counter())[off] += 1
            all_off[off] += 1
        self.group_offsets = {g: c.most_common(1)[0][0] for g, c in per_group.items()}
        self.offsets_seen = set(all_off) or {0}

    def episode(self, title, max_ep=None):
        p = parse(title)
        raw = p['sxe_ep'] if p['sxe_ep'] is not None else p['bare']
        if raw is None:
            return None, 'no episode number'
        g = p['group'] or '(none)'
        if g in self.group_offsets:
            return raw - self.group_offsets[g], 'known group'
        cands = []
        for off in self.offsets_seen:
            ep = raw - off
            if ep < 1 or (max_ep and ep > max_ep):
                continue
            cands.append(ep)
        if not cands:
            return None, 'no plausible offset'
        return min(cands), 'inferred'

    def score(self, title, target_ep, max_ep=None):
        """Confidence that this title is a good release of target_ep."""
        sc = title_score(self.aliases, title)
        if sc < 0.6:
            return 0.0, 'title miss'
        ep, why = self.episode(title, max_ep)
        if ep is None:
            # matches the show but we can't read the number -> MOST informative
            return 0.5, why
        if ep != target_ep:
            return 0.0, f'episode {ep} != {target_ep}'
        return sc, 'ok'

    def accept(self, title, ep):
        self.examples.append((title, ep))
        self.aliases |= set(extract_aliases(title))
        self._refit()

    def reject(self, title):
        self.rejected.append(title)


# ---------- the loop ----------

def propose(model, pool, target_ep, max_ep, n=3, exclude=()):
    """Rank pool, return the n most informative candidates."""
    scored = []
    for r in pool:
        if r['id'] in exclude:
            continue
        s, why = model.score(r['title'], target_ep, max_ep)
        if s <= 0:
            continue
        # informativeness: prefer UNCERTAIN (0.5) over certain
        uncertainty = 1.0 - abs(s - 0.5) * 2
        scored.append((uncertainty, s, r, why))
    scored.sort(key=lambda x: (-x[0], -x[1]))
    return scored[:n]


def simulate(canonical, seed_id, target_ep, truth_ids, max_ep=30, rounds=4):
    pool = load_pool(None)
    seed = REFS[seed_id]['title']
    model = Model(canonical, seed, target_ep)
    asked = {seed_id}

    print(f"\n=== {canonical}")
    print(f"    seed: {seed[:78]}")
    print(f"    pool: {len(pool)} candidates")

    for rnd in range(1, rounds + 1):
        cands = propose(model, pool, target_ep, max_ep, n=3, exclude=asked)
        if not cands:
            print(f"    round {rnd}: nothing left to ask")
            break
        print(f"\n    round {rnd} — tool proposes:")
        answers = []
        for unc, s, r, why in cands:
            asked.add(r['id'])
            # ground truth: is this one of the user's accepted releases?
            truth = r['id'] in truth_ids
            answers.append((r, truth, why))
            mark = 'YES' if truth else 'no '
            print(f"      [{mark}] {r['title'][:74]}")
            print(f"             ({why})")
        for r, truth, why in answers:
            if truth:
                model.accept(r['title'], target_ep)
            else:
                model.reject(r['title'])
        print(f"    -> offsets now {sorted(model.offsets_seen)}, "
              f"groups {model.group_offsets}")

    # final: does the model correctly classify the whole truth set?
    print(f"\n    final check against all {len(truth_ids)} accepted releases:")
    ok = 0
    for vid in truth_ids:
        ep, why = model.episode(REFS[vid]['title'], max_ep)
        good = ep == target_ep
        ok += good
        print(f"      [{'PASS' if good else 'FAIL'}] {vid} -> {ep} ({why})")
    print(f"    => {ok}/{len(truth_ids)}")
    return ok, len(truth_ids)


def main():
    tot_ok = tot = 0
    o, t = simulate('BLEACH: Thousand-Year Blood War - The Calamity', '2156727', 7,
                    {'2156727', '2156721', '2156732', '2156731', '2156708'}, max_ep=30)
    tot_ok += o; tot += t
    o, t = simulate('Re:ZERO -Starting Life in Another World- Season 4', '2155243', 15,
                    {'2155243', '1949681', '1955094'}, max_ep=30)
    tot_ok += o; tot += t
    o, t = simulate('Tomb Raider King', '2155442', 9,
                    {'2155442', '2155449', '2155440'}, max_ep=30)
    tot_ok += o; tot += t
    print(f"\n===== {tot_ok}/{tot} overall")


if __name__ == '__main__':
    main()
