curl -o trace_ws.out "http://localhost:6060/debug/pprof/trace?seconds=60" &
 curl -o trace_me.out "http://localhost:6061/debug/pprof/trace?seconds=60" &
  curl -o trace_fr.out "http://192.168.1.117:6060/debug/pprof/trace?seconds=60" &
  wait

~/go/bin/gotraceui trace_ws.out &
~/go/bin/gotraceui trace_me.out &
~/go/bin/gotraceui trace_fr.out &