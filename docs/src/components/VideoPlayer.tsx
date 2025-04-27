import React, { useRef, useState, useEffect } from 'react';
import styles from "../css/videoPlayer.module.scss";

interface VideoPlayerProps {
  src: string;
  alt: string;
  defaultFrame?: number;
}

declare global {
  interface Window {
    posthog?: {
      capture: (eventName: string, properties?: Record<string, any>) => void;
    };
  }
}

const VideoPlayer: React.FC<VideoPlayerProps> = ({ src, alt, defaultFrame = 5 }) => {
  const videoRef = useRef<HTMLVideoElement>(null);
  const [isPlaying, setIsPlaying] = useState(false);

  useEffect(() => {
    // Add debug logging
    // console.log('PostHog available:', !!window.posthog);
    // console.log('PostHog object:', window.posthog);

    if (videoRef.current) {
      // Set initial frame when metadata is loaded
      videoRef.current.addEventListener('loadedmetadata', () => {
        // Assuming 24fps, calculate time for the default frame
        videoRef.current.currentTime = (defaultFrame / 24);
      });
    }
  }, [defaultFrame]);

  // Helper function for event capture
  const captureEvent = (eventName: string, properties: Record<string, any>) => {
    if (!window.posthog) {
      // console.warn('PostHog not initialized when capturing:', eventName);
      return;
    }

    window.posthog.capture(eventName, properties);
    // console.log('Event captured:', eventName, properties);
  };

  const handlePlayPause = () => {
    if (videoRef.current) {
      if (isPlaying) {
        videoRef.current.pause();
        captureEvent('video_paused', {
          video_src: src,
          video_alt: alt,
          current_time: videoRef.current.currentTime,
          duration: videoRef.current.duration
        });
      } else {
        videoRef.current.play();
        captureEvent('video_played', {
          video_src: src,
          video_alt: alt,
          current_time: videoRef.current.currentTime,
          duration: videoRef.current.duration
        });
      }
      setIsPlaying(!isPlaying);
    }
  };

  const handleStop = () => {
    if (videoRef.current) {
      videoRef.current.pause();
      videoRef.current.currentTime = (defaultFrame / 24);
      setIsPlaying(false);
      captureEvent('video_stopped', {
        video_src: src,
        video_alt: alt,
        current_time: videoRef.current.currentTime,
        duration: videoRef.current.duration
      });
    }
  };

  const handleVideoClick = (e: React.MouseEvent<HTMLVideoElement>) => {
    // Only handle click if it's directly on the video element, not on controls
    if (e.target === videoRef.current) {
      captureEvent('video_link_clicked', {
        video_src: src,
        video_alt: alt
      });
      window.open(src, '_blank');
    }
  };

  return (
    <div className={styles.videoPlayerContainer}>
      <video
        ref={videoRef}
        className={styles.video}
        onClick={handleVideoClick}
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
          aria-label={isPlaying ? "Pause" : "Play"}
        >
          {isPlaying ? "⏸" : "▶"}
        </button>
        <button
          onClick={handleStop}
          className={styles.controlButton}
          aria-label="Stop"
        >
          ⏹
        </button>
      </div>
    </div>
  );
};

export default VideoPlayer;
