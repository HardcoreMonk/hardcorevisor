import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import StatusBadge from './StatusBadge'

describe('StatusBadge', () => {
  it('renders state text', () => {
    render(<StatusBadge state="running" />)
    expect(screen.getByText('running')).toBeInTheDocument()
  })

  it('applies green style for running', () => {
    render(<StatusBadge state="running" />)
    const badge = screen.getByText('running')
    expect(badge.className).toContain('bg-green')
  })

  it('applies red style for error', () => {
    render(<StatusBadge state="error" />)
    const badge = screen.getByText('error')
    expect(badge.className).toContain('bg-red')
  })

  it('handles unknown state gracefully', () => {
    render(<StatusBadge state="migrating" />)
    expect(screen.getByText('migrating')).toBeInTheDocument()
  })
})
