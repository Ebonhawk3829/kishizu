-- kishizu-watch.lua: tell kishizu when an episode has been watched.
--
-- Runs on the user's PC inside mpv. Posts the file path to kishizu wherever
-- it is reachable; kishizu does the matching, so the script never needs to
-- know show names, offsets or episode numbers.
--
-- Signal rules:
--   * An episode counts as watched when playback reaches within
--     mark_window seconds of the end (the user skips the ED), OR playback
--     reaches the actual end.
--   * The signal is posted the moment the mark window is entered, not at
--     exit. Posting mid-playback means curl always runs to completion, so
--     there is no teardown race and nothing pending when mpv quits. A video
--     abandoned before the window never sends anything, and a playlist
--     sends one signal per episode as each one reaches the window.
--
-- Failure path: if the POST fails, the payload is appended to a spool file
-- and retried on the next file load. A missed signal leaves a file on disk,
-- which is the safe direction; a blocking one would ruin mpv. A notification
-- is also sent to ntfy so the failure is visible rather than silent.

local mp = require 'mp'
local utils = require 'mp.utils'
local options = require 'mp.options'

local o = {
    -- kishizu's /api/watched endpoint on the tailnet.
    endpoint = 'http://100.64.0.1:8098/api/watched',
    -- Only files under this directory are reported. mpv is used for all media
    -- on this machine, so without the gate every film and TV episode would be
    -- posted to kishizu and come back as a 422. Subdirectories count.
    -- Empty disables the filter and reports everything.
    root = 'C:\\Anime',
    -- Seconds from the end within which playback counts as watched. The user
    -- skips the ED, so "reached the end" alone would miss most episodes.
    mark_window = 120,
    -- ntfy topic for failure alerts. Empty disables.
    ntfy = 'http://100.64.0.1:8085/kishizu',
    -- Where failed posts are spooled for retry.
    spool = mp.command_native({'expand-path', '~~state/kishizu-spool.txt'}),
}
options.read_options(o)

-- Announce the effective config on every mpv start: if the script is stale,
-- in the wrong directory, or pointing at the wrong host, the first line of
-- the console says so instead of leaving silence that looks like a bug.
mp.msg.info('kishizu-watch loaded: endpoint=' .. o.endpoint .. ' root=' ..
            (o.root == '' and '(none)' or o.root))

-- norm folds a path for comparison: backslashes to forward slashes, and
-- lowercased, because Windows is case-insensitive and mpv may hand back
-- either separator.
local function norm(p)
    if not p then return nil end
    return (p:gsub('\\', '/'):lower())
end

-- under_root reports whether a path sits inside o.root or a subdirectory of
-- it. A path with no directory component is not under anything, so it is
-- skipped: better to miss a signal than to post every loose file.
local function under_root(p)
    if o.root == '' then return true end
    local path, root = norm(p), norm(o.root)
    if not path or not root then return false end
    root = root:gsub('/+$', '') -- tolerate a trailing separator
    if root == '' then return true end
    return path:sub(1, #root) == root
        and (path:sub(#root + 1, #root + 1) == '/' or #path == #root)
end

-- marked holds one entry per file that has been posted, so the observer —
-- which fires many times per second — sends exactly one signal per episode.
local marked = {}

local function spool(payload)
    local f = io.open(o.spool, 'a')
    if f then
        f:write(payload .. '\n')
        f:close()
    end
end

local function notify(msg)
    if o.ntfy == '' then return end
    mp.command_native_async({
        name = 'subprocess',
        playback_only = false,
        args = {'curl', '-s', '-m', '5', '-d', msg, o.ntfy},
    }, function() end)
end

-- post sends one payload.
--
-- Returns one of three outcomes, because they need different handling:
--   "ok"       — 2xx, the server accepted the signal.
--   "rejected" — 4xx, the server understood it and refused it. In practice
--                this means the file is not a tracked show. Retrying can
--                never change that answer, so the payload is dropped.
--   "failed"   — curl could not run, or the server errored. Worth retrying.
--
-- Collapsing "rejected" into "failed" is what made an untracked file look
-- like an outage: the old code treated any non-2xx as unreachable.
local function post(payload)
    local proc = mp.command_native({
        name = 'subprocess',
        playback_only = false,
        capture_stdout = true,
        args = {'curl', '-s', '-o', '/dev/null', '-w', '%{http_code}',
                '-X', 'POST', '-H', 'Content-Type: application/json',
                '-d', payload, o.endpoint},
    })
    -- command_native returns a table; status 0 means curl ran.
    if not proc or proc.status ~= 0 or not proc.stdout then
        return 'failed'
    end
    local code = proc.stdout:match('(%d%d%d)')
    if not code then return 'failed' end
    if code:sub(1, 1) == '2' then return 'ok' end
    if code:sub(1, 1) == '4' then return 'rejected' end
    return 'failed'
end

-- flush_spool retries anything left from a previous session.
local function flush_spool()
    local f = io.open(o.spool, 'r')
    if not f then return end
    -- Dedupe before retrying: the spool appends on every failure, so the
    -- same payload can accumulate across sessions and one entry would be
    -- re-posted once per copy.
    local seen, lines = {}, {}
    for line in f:lines() do
        if not seen[line] then
            seen[line] = true
            table.insert(lines, line)
        end
    end
    f:close()
    if #lines == 0 then return end

    local remaining = {}
    for _, payload in ipairs(lines) do
        -- Only transport failures are worth keeping. A 4xx will never
        -- succeed on retry, so it is dropped here as well: otherwise one
        -- untracked file would be re-posted on every mpv start forever.
        if post(payload) == 'failed' then
            table.insert(remaining, payload)
        end
    end
    if #remaining == 0 then
        os.remove(o.spool)
    else
        local out = io.open(o.spool, 'w')
        if out then
            for _, payload in ipairs(remaining) do out:write(payload .. '\n') end
            out:close()
        end
    end
end

local function check_position()
    -- Read time-pos fresh inside the callback instead of trusting the
    -- observer's argument. During a playlist transition mpv can deliver a
    -- stale time-pos from the previous file alongside the next file's path;
    -- pairing those two once marked episodes that were never watched. Fresh
    -- property reads are always self-consistent.
    local pos = mp.get_property_number('time-pos')
    if not pos then return end
    local path = mp.get_property('path')
    if not path or not under_root(path) then return end
    if marked[path] then return end
    local dur = mp.get_property_number('duration')
    if not dur or dur == 0 then return end
    if dur - pos > o.mark_window then return end

    -- The mark window IS the watch signal: post now, mid-playback, while
    -- curl can run to completion. Nothing is deferred to exit, so quitting
    -- can never kill a post in flight.
    marked[path] = true
    mp.msg.info('kishizu: mark window reached, posting: ' .. path)
    local payload = utils.format_json({path = path})
    local result = post(payload)
    if result == 'ok' then
        mp.osd_message('kishizu: marked watched')
        mp.msg.info('kishizu: marked watched: ' .. path)
    elseif result == 'rejected' then
        -- The server is fine, it just does not track this show. Not an
        -- error, and not worth a phone notification: mpv plays plenty of
        -- things kishizu has never heard of.
        mp.msg.verbose('kishizu: ignored untracked file: ' .. path)
    else
        spool(payload)
        notify('kishizu: could not reach server; watch signal spooled for ' ..
               mp.get_property('filename', path))
        mp.msg.warn('kishizu: post failed, spooled: ' .. path)
    end
end

local function on_file_load()
    -- Outside the anime root this session is none of kishizu's business: no
    -- signal, no spool entry. The spool is still flushed, so a failed anime
    -- signal gets retried even if the next thing played is a film.
    local path = mp.get_property('path')
    if not under_root(path) then
        mp.msg.verbose('kishizu: ignoring ' .. tostring(path) ..
                       ' (outside ' .. o.root .. ')')
    end
    flush_spool()
end

-- No shutdown handler: every signal is posted the moment its mark window is
-- entered, so teardown has nothing to do. Posting during mpv's teardown was
-- the old design's flaw — curl could be killed after the server received the
-- request but before the status code came back, which spooled a signal the
-- server had already accepted and raised a false "could not reach server"
-- alert.

mp.register_event('start-file', on_file_load)
mp.observe_property('time-pos', 'number', check_position)