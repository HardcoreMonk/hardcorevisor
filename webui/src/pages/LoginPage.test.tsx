import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import LoginPage from './LoginPage'

function renderWithProviders(ui: React.ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>{ui}</MemoryRouter>
    </QueryClientProvider>
  )
}

describe('LoginPage', () => {
  it('renders login form', () => {
    renderWithProviders(<LoginPage />)
    expect(screen.getByText('HardCoreVisor')).toBeInTheDocument()
    expect(screen.getByText('Sign In')).toBeInTheDocument()
  })

  it('has username and password inputs', () => {
    renderWithProviders(<LoginPage />)
    expect(screen.getByLabelText('Username')).toBeInTheDocument()
    expect(screen.getByLabelText('Password')).toBeInTheDocument()
  })

  it('username input is required', () => {
    renderWithProviders(<LoginPage />)
    const input = screen.getByLabelText('Username') as HTMLInputElement
    expect(input.required).toBe(true)
  })
})
