-- kishizu-watch.lua: tell kishizu when an episode has been watched.
--
-- Runs on the user's PC inside mpv. Posts the file path to kishizu on a
-- tailnet device; kishizu does the matching, so the script never needs to know
-- show names, offsets or episode numbers.
--
-- Signal rules:
--   * An episode counts as watched when playback reaches within
--     MARK_WINDOW seconds of the end (the user skips the ED), OR playback
--     reaches the actual end.
--   * Nothing is posted while watching. The flag is checked once, on exit, so
--     a video abandoned at 20% never sends anything.
--
-- Failure path: if the POST fails, the payload is appended to a spool file and
-- retried the next time mpv starts. A missed signal leaves a file on disk,
-- which is the safe direction; a blocking one would ruin mpv. A notification
-- is also sent to ntfy so the failure is visible rather than silent.

local mp = require 'mp'
local utils = require 'mp.utils'
local options = require 'mp.options'

local o = {
    -- kishizu endpoint on the tailnet.
    endpoint = 'http://100.64.0.1:8098/api/watched',
    -- Seconds from the end within which playback counts as watched. The user
    -- skips the ED, so "reached the end" alone would miss most episodes.
    mark_window = 120,
    -- ntfy topic for failure alerts. Empty disables.
    ntfy = 'http://100.64.0.1:8085/kishizu',
    -- Where failed posts are spooled for retry.
    spool = mp.command_native({'expand-path', '~~state/kishizu-spool.txt'}),
}
options.read_options(o)

-- watched is set once playback gets close enough to the end.
local watched = false
local path = nil

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

-- post sends one payload. Returns true on success.
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
    if proc and proc.status == 0 and proc.stdout and proc.stdout:find('^2') then
        return true
    end
    return false
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
        if not post(payload) then table.insert(remaining, payload) end
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
    if watched or not pos then return end
    local dur = mp.get_property_number('duration')
    if not dur or dur == 0 then return end
    if dur - pos <= o.mark_window then
        watched = true
        mp.msg.info('kishizu: near end, will mark watched on exit')
    end
end

local function on_file_load()
    watched = false
    path = mp.get_property('path')
    flush_spool()
end

local function on_exit()
    if not watched or not path then return end
    local payload = utils.format_json({path = path})
    if post(payload) then
        mp.msg.info('kishizu: marked watched: ' .. path)
    else
        spool(payload)
        notify('kishizu: could not reach server; watch signal spooled for ' ..
               (path and mp.get_property('filename') or 'file'))
        mp.msg.warn('kishizu: post failed, spooled')
    end
end

mp.register_event('start-file', on_file_load)
mp.observe_property('time-pos', 'number', check_position)
mp.register_event('shutdown', on_exit)
mp.register_event('quit', on_exit)
