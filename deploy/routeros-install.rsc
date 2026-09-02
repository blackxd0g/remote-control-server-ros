# Remote Control Server automated deployment for RouterOS 7.22+
# Review every value in this block before importing the script.
:local image "blackxdog/remote-control-server-ros:latest"
:local containerName "remote_control_server"
:local envList "REMOTE_CONTROL_SERVER_ENV"
:local mountList "remote_control_server_data"
:local vethName "veth-remote-control-server"
:local containerAddress "172.31.255.2/30"
:local containerIP "172.31.255.2"
:local routerAddress "172.31.255.1/30"
:local routerIP "172.31.255.1"
:local storageChoice ""
:local dataRoot ""
:local publicHost ""
:local trustedProxies ""
:local adminUsername "admin"
:local adminPassword ""
:local wanList "WAN"

:while (($storageChoice != "usb1") && ($storageChoice != "system")) do={
    :put "Select container storage: usb1 or system"
    :set storageChoice [/terminal ask]
}
:if ($storageChoice = "usb1") do={
    :if ([:len [/disk find where slot="usb1"]] = 0) do={
        :error "usb1 disk was not found; attach it or run again and select system"
    }
    :set dataRoot "usb1/Containers/remote-control-server"
} else={
    :set dataRoot "Containers/remote-control-server"
}
:put ("Container storage selected: " . $storageChoice . "; data root: " . $dataRoot)

:put "Enter public DNS hostname (example: remote.example.net):"
:set publicHost [/terminal ask]
:if ([:len $publicHost] = 0) do={ :error "Public DNS hostname cannot be empty" }
:put "Enter bootstrap admin username or press Enter to use admin:"
:set adminUsername [/terminal ask]
:if ([:len $adminUsername] = 0) do={ :set adminUsername "admin" }
:put "Enter optional bootstrap admin password or press Enter to generate a one-time password:"
:set adminPassword [/terminal ask]
:put ("Public host: " . $publicHost . "; bootstrap admin: " . $adminUsername)

:local architecture [/system resource get architecture-name]
:if (($architecture != "arm64") && ($architecture != "x86_64")) do={
    :error ("Unsupported architecture: " . $architecture . ". Supported: arm64 and x86_64")
}
:if ([:len [/system package find where name="container" and disabled=no]] = 0) do={
    :error "RouterOS container package is not installed or enabled"
}
:if ([/system device-mode get container] = false) do={
    :error "Container mode is disabled; enable it with /system/device-mode/update container=yes"
}
:if ([:len [/container find where name=$containerName]] > 0) do={ :error "Container already exists; use routeros-update.rsc" }

:put ("Detected RouterOS architecture: " . $architecture)
:put "Docker Hub will select the matching linux/amd64 or linux/arm64 image."

:local stamp ([/system clock get date] . "-" . [:pick [/system clock get time] 0 2] . [:pick [/system clock get time] 3 5] . [:pick [/system clock get time] 6 8])
:local rootDir ($dataRoot . "/root-" . $stamp)
:local dataDir ($dataRoot . "/data")
:local tmpDir ($dataRoot . "/tmp")

:if ([:len [/file find where name=$dataRoot]] = 0) do={ /file add name=$dataRoot type=directory }
:if ([:len [/file find where name=$dataDir]] = 0) do={ /file add name=$dataDir type=directory }
:if ([:len [/file find where name=$tmpDir]] = 0) do={ /file add name=$tmpDir type=directory }

:if ([:len [/interface veth find where name=$vethName]] = 0) do={
    /interface veth add name=$vethName address=$containerAddress gateway=$routerIP
}
:if ([:len [/ip address find where interface=$vethName]] = 0) do={
    /ip address add address=$routerAddress interface=$vethName comment="Remote Control Server container gateway"
}

/container envs remove [find where list=$envList]
/container envs add list=$envList key="RDS_DATA_DIR" value="/data"
/container envs add list=$envList key="RDS_DB_DRIVER" value="sqlite"
/container envs add list=$envList key="RDS_REQUIRE_LOGIN" value="true"
/container envs add list=$envList key="RDS_REGISTRATION_ENABLED" value="true"
/container envs add list=$envList key="RDS_RELAY_SERVER" value=($publicHost . ":21117")
/container envs add list=$envList key="RDS_BOOTSTRAP_ADMIN_USERNAME" value=$adminUsername
:if ([:len $adminPassword] > 0) do={ /container envs add list=$envList key="RDS_BOOTSTRAP_ADMIN_PASSWORD" value=$adminPassword }
:if ([:len $trustedProxies] > 0) do={ /container envs add list=$envList key="RDS_TRUSTED_PROXIES" value=$trustedProxies }

/container mounts remove [find where list=$mountList]
/container mounts add list=$mountList src=$dataDir dst="/data" comment="Remote Control Server persistent data"
/container config set registry-url="https://registry-1.docker.io" tmpdir=$tmpDir

:if ([:len [/ip firewall nat find where comment="RCS outbound NAT"]] = 0) do={
    /ip firewall nat add chain=srcnat action=masquerade src-address=$containerIP comment="RCS outbound NAT"
}
:if ([:len [/ip firewall nat find where comment="RCS TCP services"]] = 0) do={
    /ip firewall nat add chain=dstnat action=dst-nat in-interface-list=$wanList protocol=tcp dst-port=21115-21119 to-addresses=$containerIP comment="RCS TCP services"
}
:if ([:len [/ip firewall nat find where comment="RCS UDP rendezvous"]] = 0) do={
    /ip firewall nat add chain=dstnat action=dst-nat in-interface-list=$wanList protocol=udp dst-port=21116 to-addresses=$containerIP comment="RCS UDP rendezvous"
}
:if ([:len [/ip firewall filter find where comment="RCS allow TCP services"]] = 0) do={
    /ip firewall filter add chain=forward action=accept in-interface-list=$wanList protocol=tcp dst-address=$containerIP dst-port=21115-21119 place-before=0 comment="RCS allow TCP services"
}
:if ([:len [/ip firewall filter find where comment="RCS allow UDP rendezvous"]] = 0) do={
    /ip firewall filter add chain=forward action=accept in-interface-list=$wanList protocol=udp dst-address=$containerIP dst-port=21116 place-before=0 comment="RCS allow UDP rendezvous"
}

/container add remote-image=$image interface=$vethName root-dir=$rootDir mountlists=$mountList envlists=$envList name=$containerName start-on-boot=yes logging=no stop-signal=15-SIGTERM stop-time=10s
:put "Image download/extraction started. Wait until the container is STOPPED, then run:"
:put ("/container/start [find where name=\"" . $containerName . "\"]")
:put ("Bootstrap credentials: " . $dataDir . "/secrets/bootstrap-admin.txt")
:put "Expose TCP 21114 only through a trusted HTTPS reverse proxy."
