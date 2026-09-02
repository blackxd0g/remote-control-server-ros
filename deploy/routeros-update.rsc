# Safe Remote Control Server update for RouterOS 7.22+
# Uses the official update command and preserves the /data mount.
:local containerName "remote_control_server"
:local timeoutSeconds 600
:local id [/container find where name=$containerName]
:if ([:len $id] = 0) do={ :error "Container not found" }

:put "Stopping Remote Control Server..."
/container stop $id
:delay 5s
:put "Downloading and extracting the current remote-image tag..."
/container update $id

:local waited 0
:while (($waited < $timeoutSeconds) && ([/container get $id status] != "stopped")) do={
    :delay 5s
    :set waited ($waited + 5)
}
:if ([/container get $id status] != "stopped") do={ :error "Update timed out; inspect /container/print detail" }

/container start $id
:delay 5s
:put ("Remote Control Server status: " . [/container get $id status])
