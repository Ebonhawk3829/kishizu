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
--   * The signal is posted the moment the mark window is entered, and the
--     post runs asynchronously so it never blocks mpv. A video abandoned
--     before the window never sends anything, and a playlist sends one
--     signal per episode as each one reaches the window.
--
-- Failure path: a post whose outcome is unclear is settled by asking the
-- server whether the episode is marked watched. Only a confirmed miss is
-- spooled and reported; a signal that landed is dropped silently. Marking an
-- episode watched is idempotent, so a retry is free either way.

local mp = require 'mp'
local utils = require 'mp.utils'
local options = require 'mp.options'

local o = {
    -- kishizu's /api/watched endpoint on the tailnet.
    endpoint = 'http://100.64.0.1:8098/api/watched',
    -- Read-only companion to endpoint: reports whether an episode is marked
    -- watched, so an unclear post can be settled by asking.
    verify_endpoint = 'http://100.64.0.1:8098/api/watched/verify',
    -- Only files under this directory are reported. mpv is used for all media
    -- on this machine, so without the gate every film and TV episode would be
    -- posted to kishizu and come back as a 422. Subdirectories count.
    -- Empty disables the filter and reports everything.
    root = 'C:\\Anime',
    -- Seconds from the end within which playback counts as watched. The user
    -- skips the ED, so "reached the end" alone would miss most episodes.
    mark_window = 120,
    -- Seconds to wait before verifying an unclear post: long enough for the
    -- server to record the watch and run its sweep.
    verify_delay = 5,
    -- ntfy topic for failure alerts. Empty disables.
    ntfy = 'http://100.64.0.1:8085/kishizu',
    -- Where unsettled posts are spooled for retry.
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

-- in_flight holds payloads whose result has not come back yet. Shutdown
-- spools whatever is still here; see on_shutdown for why that is silent.
local in_flight = {}

-- request runs one curl call and hands the parsed result to a callback.
--
-- Asynchronous throughout: a synchronous subprocess holds mpv's main thread
-- until curl exits, so anything that ends the session mid-call takes curl
-- with it and the result is lost. Running off the main thread keeps the
-- subprocess alive and delivers the result to the callback instead.
local function request(url, payload, done)
    mp.command_native_async({
        name = 'subprocess',
        playback_only = false,
        capture_stdout = true,
        args = {'curl', '-s', '-m', '10', '-w', '\n%{http_code}',
                '-X', 'POST', '-H', 'Content-Type: application/json',
                '-d', payload, url},
    }, function(success, res, _)
        if not success or not res or res.status ~= 0 or not res.stdout then
            done(nil)
            return
        end
        -- Body and status share stdout, separated by the newline written
        -- above; the status is the last line.
        local body, code = res.stdout:match('^(.-)\n?(%d%d%d)%s*$')
        if not code then
            done(nil)
            return
        end
        done({body = body or '', code = code})
    end)
end

-- post_async sends one watch signal without blocking mpv.
--
-- Three outcomes, because they need different handling:
--   "ok"       — 2xx, the server accepted the signal.
--   "rejected" — 4xx, the server understood it and refused it. In practice
--                this means the file is not a tracked show, so retrying can
--                never change the answer and the payload is dropped.
--   "failed"   — curl could not run, or the server errored. The request may
--                still have landed, so the outcome is settled by asking the
--                server rather than assumed.
local function post_async(payload, done)
    in_flight[payload] = true
    request(o.endpoint, payload, function(res)
        in_flight[payload] = nil
        if not res then
            done('failed')
            return
        end
        local c = res.code:sub(1, 1)
        if c == '2' then
            done('ok')
        elseif c == '4' then
            done('rejected')
        else
            done('failed')
        end
    end)
end

-- verify_async asks the server whether an episode is marked watched.
--
-- Reports true only on a positive answer. An unreachable server or an
-- unparseable reply reports false, which spools the signal: a retry is free,
-- while dropping a signal that never landed leaves a file on disk forever.
local function verify_async(payload, done)
    request(o.verify_endpoint, payload, function(res)
        if not res or res.code:sub(1, 1) ~= '2' then
            done(false)
            return
        end
        done(res.body:find('"watched"%s*:%s*true') ~= nil)
    end)
end

-- settle resolves a post whose outcome is unclear: wait for the server to
-- finish recording, then ask whether the episode is marked. A confirmed
-- watch needs no further action; anything else is spooled and reported.
local function settle(payload, filename)
    mp.add_timeout(o.verify_delay, function()
        verify_async(payload, function(watched)
            if watched then
                mp.msg.info('kishizu: verified watched: ' .. filename)
                return
            end
            spool(payload)
            notify('kishizu: could not reach server; watch signal spooled for ' ..
                   filename)
            mp.msg.warn('kishizu: post unsettled, spooled: ' .. filename)
        end)
    end)
end

-- write_spool replaces the spool file with the given payloads, or removes
-- it when none are left.
local function write_spool(remaining)
    if #remaining == 0 then
        os.remove(o.spool)
        return
    end
    local out = io.open(o.spool, 'w')
    if out then
        for _, payload in ipairs(remaining) do out:write(payload .. '\n') end
        out:close()
    end
end

-- flush_spool retries anything left from a previous session.
local function flush_spool()
    local f = io.open(o.spool, 'r')
    if not f then return end
    -- Dedupe before retrying: the spool appends on every unsettled post, so
    -- the same payload can accumulate across sessions and one entry would be
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

    -- Rewritten only once every retry has settled, so an interrupted flush
    -- leaves the spool intact rather than half-truncated.
    local remaining, pending = {}, #lines
    for _, payload in ipairs(lines) do
        post_async(payload, function(result)
            -- Only transport failures are worth keeping. A 4xx will never
            -- succeed on retry, so it is dropped here as well: otherwise one
            -- untracked file would be re-posted on every mpv start forever.
            if result == 'failed' then
                table.insert(remaining, payload)
            end
            pending = pending - 1
            if pending == 0 then write_spool(remaining) end
        end)
    end
end

local function check_position()
    -- Read time-pos fresh inside the callback rather than trusting the
    -- observer's argument. Property reads inside a callback are always
    -- self-consistent, whereas the delivered value can belong to the
    -- previous file during a playlist transition.
    local pos = mp.get_property_number('time-pos')
    if not pos then return end
    local path = mp.get_property('path')
    if not path or not under_root(path) then return end
    if marked[path] then return end
    local dur = mp.get_property_number('duration')
    if not dur or dur == 0 then return end
    if dur - pos > o.mark_window then return end

    -- The mark window IS the watch signal: post now, mid-playback, rather
    -- than deferring to exit. The post is asynchronous, so it neither blocks
    -- mpv nor depends on mpv staying alive for curl to finish.
    marked[path] = true
    mp.msg.info('kishizu: mark window reached, posting: ' .. path)
    local payload = utils.format_json({path = path})
    local filename = mp.get_property('filename', path)
    post_async(payload, function(result)
        if result == 'ok' then
            mp.osd_message('kishizu: marked watched')
            mp.msg.info('kishizu: marked watched: ' .. path)
        elseif result == 'rejected' then
            -- The server is fine, it just does not track this show. Not an
            -- error, and not worth a phone notification: mpv plays plenty of
            -- things kishizu has never heard of.
            mp.msg.verbose('kishizu: ignored untracked file: ' .. path)
        else
            settle(payload, filename)
        end
    end)
end

local function on_file_load()
    -- Outside the anime root this session is none of kishizu's business: no
    -- signal, no spool entry. The spool is still flushed, so an unsettled
    -- anime signal gets retried even if the next thing played is a film.
    local path = mp.get_property('path')
    if not under_root(path) then
        mp.msg.verbose('kishizu: ignoring ' .. tostring(path) ..
                       ' (outside ' .. o.root .. ')')
    end
    flush_spool()
end

-- on_shutdown spools any post whose result never came back.
--
-- Deliberately silent: whether the request reached the server is unknowable
-- from here, and the next session's flush settles it by asking. Reporting it
-- as an outage would raise an alert for a signal that may well have landed.
local function on_shutdown()
    for payload in pairs(in_flight) do
        spool(payload)
    end
    in_flight = {}
end

mp.register_event('start-file', on_file_load)
mp.observe_property('time-pos', 'number', check_position)
mp.register_event('shutdown', on_shutdown)
mp.register_event('quit', on_shutdown)