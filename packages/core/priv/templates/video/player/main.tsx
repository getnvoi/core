// The preview people play before the video is rendered: the Remotion Player on the same composition and props.
import { Player } from "@remotion/player";
import { createRoot } from "react-dom/client";
import props from "../props.json";
import { Main } from "../src/Main";
import { FPS, HEIGHT, WIDTH, type Props } from "../src/props";

createRoot(document.getElementById("root")!).render(
  <div style={{ display: "flex", height: "100%", alignItems: "center", justifyContent: "center", padding: 24, boxSizing: "border-box" }}>
    <Player
      component={Main}
      inputProps={props as Props}
      durationInFrames={Math.max(1, Math.round(props.seconds * FPS))}
      fps={FPS}
      compositionWidth={WIDTH}
      compositionHeight={HEIGHT}
      style={{ width: "100%", maxHeight: "100%", aspectRatio: `${WIDTH} / ${HEIGHT}`, borderRadius: 8, overflow: "hidden" }}
      controls
      loop
    />
  </div>,
);
