import { render, screen } from '@testing-library/react';
import React from 'react';

import { CountQsdmHive, TOO_SMALL_AMOUNT_PLACEHOLDER } from './CountQsdmHive';

describe('CountQsdmHive', () => {
  test('renders small amount placeholder for very small amounts', () => {
    render(<CountQsdmHive value={0.000000001} />);

    expect(screen.getByText(TOO_SMALL_AMOUNT_PLACEHOLDER)).toBeInTheDocument();
  });

  test('renders CountUp for non-small amounts', () => {
    render(<CountQsdmHive value={1000000000} />);

    const countUp = screen.getAllByText(/1.00/)[0];
    expect(countUp).toBeInTheDocument();
  });
});
