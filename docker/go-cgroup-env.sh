#!/bin/sh

if [ -z "${GOMEMLIMIT:-}" ]; then
    memory_limit=""
    for path in /sys/fs/cgroup/memory.max /sys/fs/cgroup/memory/memory.limit_in_bytes; do
        if [ -r "$path" ]; then
            memory_limit="$(cat "$path")"
            break
        fi
    done
    case "$memory_limit" in
        "" | max | *[!0-9]*)
            ;;
        *)
            if [ "$memory_limit" -gt 0 ] && [ "$memory_limit" -lt 9000000000000000000 ]; then
                memory_mib=$((memory_limit * 3 / 4 / 1048576))
                if [ "$memory_mib" -ge 64 ]; then
                    export GOMEMLIMIT="${memory_mib}MiB"
                fi
            fi
            ;;
    esac
fi

if [ -z "${GOMAXPROCS:-}" ]; then
    cpu_quota=""
    cpu_period=""
    if [ -r /sys/fs/cgroup/cpu.max ]; then
        read -r cpu_quota cpu_period < /sys/fs/cgroup/cpu.max
    elif [ -r /sys/fs/cgroup/cpu/cpu.cfs_quota_us ] && [ -r /sys/fs/cgroup/cpu/cpu.cfs_period_us ]; then
        cpu_quota="$(cat /sys/fs/cgroup/cpu/cpu.cfs_quota_us)"
        cpu_period="$(cat /sys/fs/cgroup/cpu/cpu.cfs_period_us)"
    fi
    case "$cpu_quota:$cpu_period" in
        :* | *: | max:* | *[!0123456789:]*)
            ;;
        *)
            if [ "$cpu_quota" -gt 0 ] && [ "$cpu_period" -gt 0 ]; then
                gomaxprocs=$(((cpu_quota + cpu_period - 1) / cpu_period))
                if [ "$gomaxprocs" -lt 1 ]; then
                    gomaxprocs=1
                fi
                export GOMAXPROCS="$gomaxprocs"
            fi
            ;;
    esac
fi
