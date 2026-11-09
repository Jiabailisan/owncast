import { FC, useEffect, useRef, useState } from 'react';

import styles from './PlayerControls.module.scss';

export type VolumeSliderProps = {
  value: number;
  onChange: (value: number) => void;
  onFocusChange?: (focused: boolean) => void;
};

// Custom volume slider ported from the provided player: a div track with a
// white fill and thumb. The native <input type=range> was clipped by the pill's
// rounded overflow and could not show the thumb fully at 0%/100%.
export const VolumeSlider: FC<VolumeSliderProps> = ({ value, onChange, onFocusChange }) => {
  const sliderRef = useRef<HTMLDivElement | null>(null);
  const [dragging, setDragging] = useState(false);

  const updateFromClientX = (clientX: number) => {
    const element = sliderRef.current;
    if (!element) return;
    const rect = element.getBoundingClientRect();
    if (rect.width <= 0) return;
    onChange(Math.min(1, Math.max(0, (clientX - rect.left) / rect.width)));
  };

  useEffect(() => {
    if (!dragging) return undefined;
    const onMove = (event: PointerEvent | TouchEvent) => {
      if ('touches' in event) {
        const touch = event.touches[0];
        if (touch) {
          event.preventDefault();
          updateFromClientX(touch.clientX);
        }
        return;
      }
      event.preventDefault();
      updateFromClientX(event.clientX);
    };
    const stop = () => setDragging(false);
    document.addEventListener('pointermove', onMove, true);
    document.addEventListener('touchmove', onMove, { passive: false });
    document.addEventListener('pointerup', stop);
    document.addEventListener('touchend', stop);
    document.addEventListener('touchcancel', stop);
    return () => {
      document.removeEventListener('pointermove', onMove, true);
      document.removeEventListener('touchmove', onMove);
      document.removeEventListener('pointerup', stop);
      document.removeEventListener('touchend', stop);
      document.removeEventListener('touchcancel', stop);
    };
    // updateFromClientX closes over onChange only; stable enough for a drag.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [dragging]);

  const finishDrag = (event: React.PointerEvent<HTMLDivElement>) => {
    if (event.currentTarget.hasPointerCapture?.(event.pointerId)) {
      event.currentTarget.releasePointerCapture?.(event.pointerId);
    }
    setDragging(false);
  };

  return (
    <div
      ref={sliderRef}
      className={styles.volumeSlider}
      role="slider"
      tabIndex={0}
      aria-label="Volume"
      aria-valuemin={0}
      aria-valuemax={100}
      aria-valuenow={Math.round(100 * value)}
      onPointerDown={event => {
        event.preventDefault();
        event.stopPropagation();
        event.currentTarget.setPointerCapture?.(event.pointerId);
        setDragging(true);
        updateFromClientX(event.clientX);
      }}
      onPointerMove={event => {
        if (dragging) {
          event.preventDefault();
          updateFromClientX(event.clientX);
        }
      }}
      onPointerUp={finishDrag}
      onPointerCancel={finishDrag}
      onTouchStart={event => {
        event.preventDefault();
        event.stopPropagation();
        setDragging(true);
        updateFromClientX(event.touches[0].clientX);
      }}
      onTouchMove={event => {
        if (dragging) {
          event.preventDefault();
          updateFromClientX(event.touches[0].clientX);
        }
      }}
      onTouchEnd={() => setDragging(false)}
      onTouchCancel={() => setDragging(false)}
      onFocus={() => onFocusChange?.(true)}
      onBlur={() => onFocusChange?.(false)}
      onKeyDown={event => {
        if (event.key === 'ArrowLeft' || event.key === 'ArrowDown') {
          event.preventDefault();
          onChange(Math.max(0, value - 0.05));
        } else if (event.key === 'ArrowRight' || event.key === 'ArrowUp') {
          event.preventDefault();
          onChange(Math.min(1, value + 0.05));
        } else if (event.key === 'Home') {
          event.preventDefault();
          onChange(0);
        } else if (event.key === 'End') {
          event.preventDefault();
          onChange(1);
        }
      }}
    >
      <div className={styles.volumeTrack}>
        <div className={styles.volumeFill} style={{ width: Math.round(100 * value) + '%' }} />
        <div className={styles.volumeThumb} style={{ left: Math.round(100 * value) + '%' }} />
      </div>
    </div>
  );
};

export default VolumeSlider;
