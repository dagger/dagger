describe('Visit Docs website', function() {
  
  it('Visit docs website without authentication', function() {
    cy.visit('http://localhost:3000')
    cy.get('[data-cy="cy-signin"]').should('not.exist')
  }),
  
  it('Visit docs website with authentication', function() {
    cy.visit('http://localhost:3001')
    cy.get('[data-cy="cy-signin"]').should('exist')
  })

  context('When user is authenticated', function() {
    beforeEach(() => {
      cy.setLocalStorage('user', "{\"permission\":true,\"login\":\"slumbering\"}")
      cy.intercept('/t', 'success').as('logAmplitude')
      cy.visit(('http://localhost:3001'))
    })

    it('Visit docs website with a valid authenticated user', function() {
      cy.get('[data-cy=cy-doc-content]').should('exist')
    })

    it('log to amplitude when user visit another page', function() {
      cy.get('[data-cy=cy-doc-content]').should('exist')
      cy.get('.menu > :nth-child(2) > :nth-child(2) > .menu__link').click()
    })
  })

  context('When user is not authorized', function() {
    it('Redirect user after unsuccessful sign in', function() {
      cy.intercept('**/login/oauth/access_token?code=jergub54545&client_id=123&client_secret=321', {fixture: 'bad_verification.code.json'})
      cy.intercept('**/user', (req) => {
         req.continue((res) => {
           expect(res.statusCode).to.be.equal(401)
         })
      })
      cy.visit('http://localhost:3001?code=jergub54545')
      cy.get('[data-cy=cy-page-redirect]').should('exist')
      // cy.wait(10000)
      // cy.location().should((location) => {
      //   expect(location.host).to.eq('dagger.io')
      // })
    })
    
    it('Visit docs website with a user not authorized', function() {
      cy.setLocalStorage('user', "{\"permission\":false,\"login\":\"slumbering\"}")
      cy.visit('http://localhost:3001')
      cy.get('[data-cy=cy-page-redirect]').should('exist')
      cy.intercept('/t', 'success')
    })
  })
})