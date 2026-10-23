import React, { useEffect, useRef } from "react";

interface RecordingProps {
  // A terminal recording, served from static/recordings/<id>.cast.
  id: string;
  title: string;
}

// Plays an asciicast with asciinema-player. The player loads in the browser
// only, and plays once the recording is half visible.
const Recording: React.FC<RecordingProps> = ({ id, title }) => {
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    let player: { play: () => void; dispose: () => void } | undefined;
    let observer: IntersectionObserver | undefined;
    let cancelled = false;

    Promise.all([
      import("asciinema-player"),
      import("asciinema-player/dist/bundle/asciinema-player.css"),
    ]).then(([lib]) => {
      if (cancelled) return;
      player = lib.create(`/recordings/${id}.cast`, el, {
        preload: true,
        fit: "width",
        idleTimeLimit: 2,
      });
      observer = new IntersectionObserver(
        (entries) => {
          if (entries.some((e) => e.isIntersecting)) {
            player?.play();
            observer?.disconnect();
          }
        },
        { threshold: 0.5 },
      );
      observer.observe(el);
    });

    return () => {
      cancelled = true;
      observer?.disconnect();
      player?.dispose();
    };
  }, [id]);

  return <figure ref={ref} aria-label={title} style={{ margin: "1.5rem 0" }} />;
};

export default Recording;
