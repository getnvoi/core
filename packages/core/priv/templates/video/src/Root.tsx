// The compositions the renderer knows: Main, sized and timed from props.json.
import { Composition } from "remotion";
import props from "../props.json";
import { Main } from "./Main";
import { FPS, HEIGHT, WIDTH, type Props } from "./props";

export function Root() {
  return (
    <Composition
      id="Main"
      component={Main}
      width={WIDTH}
      height={HEIGHT}
      fps={FPS}
      durationInFrames={Math.max(1, Math.round(props.seconds * FPS))}
      defaultProps={props as Props}
      calculateMetadata={({ props: p }) => ({ durationInFrames: Math.max(1, Math.round(p.seconds * FPS)) })}
    />
  );
}
