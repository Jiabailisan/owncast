import { render, screen } from '@testing-library/react';

import { VideoHintAlert } from '../components/admin/VideoHintAlert';

describe('VideoHintAlert', () => {
  test('renders the unified info hint pattern with icon and close action', () => {
    render(<VideoHintAlert message="First hint" testId="hint" />);
    const hint = screen.getByTestId('hint');
    expect(hint).toHaveClass('ant-alert');
    expect(hint).toHaveClass('ant-alert-info');
    expect(hint.querySelector('.ant-alert-icon')).not.toBeNull();
    expect(hint.querySelector('.ant-alert-close-icon')).not.toBeNull();
    expect(screen.getByText('First hint')).toBeInTheDocument();
    expect(hint.querySelector('.ant-alert-description')).toBeNull();
  });

  test('renders a description with the same content block as the message-only hint', () => {
    render(
      <VideoHintAlert
        message="Mapping hint"
        description="Copy requires H.264 without B-frames."
        testId="with-description"
      />,
    );
    const hint = screen.getByTestId('with-description');
    // Both hints share the component's typography class and the same Alert
    // structure; the description lives in the standard content block.
    expect(hint.className).toContain('hint');
    expect(hint.querySelector('.ant-alert-title')).toHaveTextContent('Mapping hint');
    expect(hint.querySelector('.ant-alert-description')).toHaveTextContent(
      'Copy requires H.264 without B-frames.',
    );
    expect(screen.getByText('Copy requires H.264 without B-frames.')).toBeInTheDocument();
  });
});
