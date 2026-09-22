-- kishizu-watch.lua: tell kishizu when an episode has been watched.
--
-- Runs on the user's PC inside mpv. Posts the file path to kishizu wherever
-- it is reachable; kishizu does the matching, so the script never needs to
-- know show names, offsets or episode numbers.
--
-- Signal rules:
--   * An episode counts as watched when playback reaches within
--     MARK_WINDOW seconds of the end (the user skips the ED), OR playback
--     reaches the actual end.
--   * Nothing is posted while watching. Each file that reaches the mark
--     window is remembered and posted once, on exit, so a video abandoned
--     at 20% never sends anything and a playlist sends one signal per
--     episode, not just the last one.
--
-- Failure path: if the POST fails, the payload is appended to a spool file and
-- retried the next time mpv starts. A missed signal leaves a file on disk,
-- which is the safe direction; a blocking one would ruin mpv. A notification
-- is also sent to ntfy so the failure is visible rather than silent.

local mp = require 'mp'
local utils = require 'mp.utils'
local options = require 'mp.options'

local o = {
    -- kishizu's /api/watched endpoint. Replace HOST with wherever kishizu is
    -- reachable from this machine: a hostname, a LAN address, or a VPN
    -- address. Set this in mpv's script-opts rather than editing the file.
    endpoint = 'http://HOST:8098/api/watched',
    -- Only files under this directory are reported. mpv is used for all media
    -- on this machine, so without the gate every film and TV episode would be
    -- posted to kishizu and come back as a 422. Subdirectories count.
    -- Empty disables the filter and reports everything.
    root = '',
    -- Seconds from the end within which playback counts as watched. The user
    -- skips the ED, so "reached the end" alone would miss most episodes.
    mark_window = 120,
    -- ntfy topic for failure alerts. Empty disables.
    ntfy = '',
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

-- pending holds one entry per file that reached the mark window, so a
-- playlist of episodes sends one signal per episode instead of only the
-- last one.
local pending = {}

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
    local lines = {}
    for line in f:lines() do table.insert(lines, line) end
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

local function check_position(_, pos)
    -- time-pos is unavailable before playback starts and at file transitions;
    -- the observer still fires then, with nil.
    if not pos then return end
    local path = mp.get_property('path')
    if not path or not under_root(path) then return end
    if pending[path] then return end
    local dur = mp.get_property_number('duration')
    if not dur or dur == 0 then return end
    if dur - pos <= o.mark_window then
        pending[path] = true
        mp.msg.info('kishizu: near end, will mark watched on exit: ' .. path)
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

local function on_exit()
    for path in pairs(pending) do
        local payload = utils.format_json({path = path})
        local result = post(payload)
        if result == 'ok' then
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
    pending = {}
end

mp.register_event('start-file', on_file_load)
mp.observe_property('time-pos', 'number', check_position)
mp.register_event('shutdown', on_exit)
mp.register_event('quit', on_exit)
