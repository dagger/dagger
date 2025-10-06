import React, { useRef, useState } from 'react';
import styles from "../css/videoPlayer.module.scss";

interface VideoPlayerProps {
  src: string;
  alt: string;
  defaultFrame?: number;
}

const VideoPlayer: React.FC<VideoPlayerProps> = ({ src, alt, defaultFrame = 5 }) => {
  const ref = useRef<HTMLVideoElement>(null);
  const [isPlaying, setIsPlaying] = useState(false);

  const handlePlayPause = () => {
    if (ref.current) {
      if (isPlaying) {
        ref.current.pause();
      } else {
        ref.current.play();
      }
      setIsPlaying(!isPlaying);
    }
  };

  const handleStop = () => {
    if (ref.current) {
      ref.current.pause();
      setIsPlaying(false);
    }
  };

  const handleClick = (e: React.MouseEvent<HTMLVideoElement>) => {
    if (e.target === ref.current) {
      window.open(src, '_blank');
    }
  };

  return (
    <div className={styles.videoPlayerContainer}>
      <video
        ref={ref}
        className={styles.video}
        onClick={handleClick}
        onEnded={() => setIsPlaying(false)}
        style={{ cursor: 'pointer' }}
      >
        <source src={src} type="video/webm" />
        {alt}
      </video>
      <div className={styles.controls}>
        <button
          onClick={handlePlayPause}
          className={styles.controlButton}
          data-ph-capture-attribute-video-file={src}
          data-ph-capture-attribute-video-title={alt}
        >
          {isPlaying ? "⏸" : "▶"}
        </button>
        <button
          onClick={handleStop}
          className={styles.controlButton}
        >
          ⏹
        </button>
      </div>
    </div>
  );
};

export default VideoPlayer;
