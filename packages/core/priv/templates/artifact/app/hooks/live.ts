import { useEffect, useRef } from "react";

import { SOCKET_PATH } from "~/lib/socket";

// Calls `handle` with every message broadcast to this viewer or to everyone.
// Reconnects after a drop, which also covers the machine pausing and waking.
export function useLive(handle: (message: unknown) => void) {
  const latest = useRef(handle);
  latest.current = handle;

  useEffect(() => {
    let socket: WebSocket | null = null;
    let retry: ReturnType<typeof setTimeout> | undefined;
    let closed = false;

    const connect = () => {
      const scheme = window.location.protocol === "https:" ? "wss:" : "ws:";
      socket = new WebSocket(`${scheme}//${window.location.host}${SOCKET_PATH}`);

      socket.onmessage = (event) => {
        try {
          latest.current(JSON.parse(event.data));
        } catch (error) {
          console.error("live:", error);
        }
      };

      socket.onclose = () => {
        if (!closed) retry = setTimeout(connect, 2000);
      };
    };

    connect();

    return () => {
      closed = true;
      clearTimeout(retry);
      socket?.close();
    };
  }, []);
}
