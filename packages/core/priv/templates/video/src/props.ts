// What the composition takes, as props.json writes it. `seconds` is the video's length at FPS frames a second.
export type Props = {
  title: string;
  subtitle: string;
  background: string;
  color: string;
  seconds: number;
};

export const FPS = 30;
export const WIDTH = 1920;
export const HEIGHT = 1080;
