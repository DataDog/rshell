//! Arithmetic expression evaluator for `$((...))` and `(( ... ))`.
//!
//! Supports the operators most scripts use: `+ - * / %`, comparison
//! (`<`, `<=`, `>`, `>=`, `==`, `!=`), logic (`&&`, `||`, `!`), bitwise
//! (`& | ^ ~ << >>`), unary (`+`, `-`, `!`, `~`), parentheses, ternary
//! `cond ? a : b`, and variable references (`x`, `$x`). Pre/post
//! inc/decrement are not supported in this baseline.

use bstr::ByteSlice;

use crate::env::Env;

#[derive(Debug, thiserror::Error)]
pub enum ArithError {
    #[error("arithmetic syntax error at position {0}")]
    Syntax(usize),
    #[error("division by zero")]
    DivByZero,
}

pub fn eval(src: &[u8], env: &Env) -> Result<i64, ArithError> {
    let mut p = Parser { src, pos: 0, env };
    p.skip_ws();
    let v = p.expr()?;
    p.skip_ws();
    if p.pos != src.len() {
        return Err(ArithError::Syntax(p.pos));
    }
    Ok(v)
}

struct Parser<'a> {
    src: &'a [u8],
    pos: usize,
    env: &'a Env,
}

impl<'a> Parser<'a> {
    fn peek(&self) -> Option<u8> {
        self.src.get(self.pos).copied()
    }

    fn skip_ws(&mut self) {
        while let Some(c) = self.peek() {
            if c.is_ascii_whitespace() {
                self.pos += 1;
            } else {
                break;
            }
        }
    }

    fn eat(&mut self, prefix: &[u8]) -> bool {
        if self.src[self.pos..].starts_with(prefix) {
            self.pos += prefix.len();
            true
        } else {
            false
        }
    }

    fn expr(&mut self) -> Result<i64, ArithError> {
        self.ternary()
    }

    fn ternary(&mut self) -> Result<i64, ArithError> {
        let cond = self.logical_or()?;
        self.skip_ws();
        if self.eat(b"?") {
            self.skip_ws();
            let a = self.expr()?;
            self.skip_ws();
            if !self.eat(b":") {
                return Err(ArithError::Syntax(self.pos));
            }
            self.skip_ws();
            let b = self.expr()?;
            Ok(if cond != 0 { a } else { b })
        } else {
            Ok(cond)
        }
    }

    fn logical_or(&mut self) -> Result<i64, ArithError> {
        let mut left = self.logical_and()?;
        loop {
            self.skip_ws();
            if self.eat(b"||") {
                let right = self.logical_and()?;
                left = (left != 0 || right != 0) as i64;
            } else {
                return Ok(left);
            }
        }
    }

    fn logical_and(&mut self) -> Result<i64, ArithError> {
        let mut left = self.bit_or()?;
        loop {
            self.skip_ws();
            if self.eat(b"&&") {
                let right = self.bit_or()?;
                left = (left != 0 && right != 0) as i64;
            } else {
                return Ok(left);
            }
        }
    }

    fn bit_or(&mut self) -> Result<i64, ArithError> {
        let mut left = self.bit_xor()?;
        loop {
            self.skip_ws();
            // Avoid eating `||`.
            if self.peek() == Some(b'|') && self.src.get(self.pos + 1) != Some(&b'|') {
                self.pos += 1;
                let right = self.bit_xor()?;
                left |= right;
            } else {
                return Ok(left);
            }
        }
    }

    fn bit_xor(&mut self) -> Result<i64, ArithError> {
        let mut left = self.bit_and()?;
        loop {
            self.skip_ws();
            if self.eat(b"^") {
                let right = self.bit_and()?;
                left ^= right;
            } else {
                return Ok(left);
            }
        }
    }

    fn bit_and(&mut self) -> Result<i64, ArithError> {
        let mut left = self.equality()?;
        loop {
            self.skip_ws();
            // Avoid eating `&&`.
            if self.peek() == Some(b'&') && self.src.get(self.pos + 1) != Some(&b'&') {
                self.pos += 1;
                let right = self.equality()?;
                left &= right;
            } else {
                return Ok(left);
            }
        }
    }

    fn equality(&mut self) -> Result<i64, ArithError> {
        let mut left = self.comparison()?;
        loop {
            self.skip_ws();
            if self.eat(b"==") {
                let right = self.comparison()?;
                left = (left == right) as i64;
            } else if self.eat(b"!=") {
                let right = self.comparison()?;
                left = (left != right) as i64;
            } else {
                return Ok(left);
            }
        }
    }

    fn comparison(&mut self) -> Result<i64, ArithError> {
        let mut left = self.shift()?;
        loop {
            self.skip_ws();
            if self.eat(b"<=") {
                let r = self.shift()?;
                left = (left <= r) as i64;
            } else if self.eat(b">=") {
                let r = self.shift()?;
                left = (left >= r) as i64;
            } else if self.peek() == Some(b'<') && self.src.get(self.pos + 1) != Some(&b'<') {
                self.pos += 1;
                let r = self.shift()?;
                left = (left < r) as i64;
            } else if self.peek() == Some(b'>') && self.src.get(self.pos + 1) != Some(&b'>') {
                self.pos += 1;
                let r = self.shift()?;
                left = (left > r) as i64;
            } else {
                return Ok(left);
            }
        }
    }

    fn shift(&mut self) -> Result<i64, ArithError> {
        let mut left = self.addsub()?;
        loop {
            self.skip_ws();
            if self.eat(b"<<") {
                let r = self.addsub()?;
                left = left.wrapping_shl(r as u32);
            } else if self.eat(b">>") {
                let r = self.addsub()?;
                left = left.wrapping_shr(r as u32);
            } else {
                return Ok(left);
            }
        }
    }

    fn addsub(&mut self) -> Result<i64, ArithError> {
        let mut left = self.muldiv()?;
        loop {
            self.skip_ws();
            match self.peek() {
                Some(b'+') => {
                    self.pos += 1;
                    let r = self.muldiv()?;
                    left = left.wrapping_add(r);
                }
                Some(b'-') => {
                    self.pos += 1;
                    let r = self.muldiv()?;
                    left = left.wrapping_sub(r);
                }
                _ => return Ok(left),
            }
        }
    }

    fn muldiv(&mut self) -> Result<i64, ArithError> {
        let mut left = self.unary()?;
        loop {
            self.skip_ws();
            match self.peek() {
                Some(b'*') => {
                    self.pos += 1;
                    let r = self.unary()?;
                    left = left.wrapping_mul(r);
                }
                Some(b'/') => {
                    self.pos += 1;
                    let r = self.unary()?;
                    if r == 0 {
                        return Err(ArithError::DivByZero);
                    }
                    left = left.wrapping_div(r);
                }
                Some(b'%') => {
                    self.pos += 1;
                    let r = self.unary()?;
                    if r == 0 {
                        return Err(ArithError::DivByZero);
                    }
                    left = left.wrapping_rem(r);
                }
                _ => return Ok(left),
            }
        }
    }

    fn unary(&mut self) -> Result<i64, ArithError> {
        self.skip_ws();
        match self.peek() {
            Some(b'+') => {
                self.pos += 1;
                self.unary()
            }
            Some(b'-') => {
                self.pos += 1;
                let v = self.unary()?;
                Ok(v.wrapping_neg())
            }
            Some(b'!') if self.src.get(self.pos + 1) != Some(&b'=') => {
                self.pos += 1;
                let v = self.unary()?;
                Ok((v == 0) as i64)
            }
            Some(b'~') => {
                self.pos += 1;
                let v = self.unary()?;
                Ok(!v)
            }
            _ => self.primary(),
        }
    }

    fn primary(&mut self) -> Result<i64, ArithError> {
        self.skip_ws();
        if self.eat(b"(") {
            let v = self.expr()?;
            self.skip_ws();
            if !self.eat(b")") {
                return Err(ArithError::Syntax(self.pos));
            }
            return Ok(v);
        }
        if let Some(c) = self.peek()
            && c.is_ascii_digit()
        {
            let start = self.pos;
            // Hex / octal / decimal.
            if self.src[self.pos..].starts_with(b"0x") || self.src[self.pos..].starts_with(b"0X") {
                self.pos += 2;
                while let Some(c) = self.peek() {
                    if c.is_ascii_hexdigit() {
                        self.pos += 1;
                    } else {
                        break;
                    }
                }
                let s = self.src[start + 2..self.pos].to_str_lossy();
                return i64::from_str_radix(&s, 16).map_err(|_| ArithError::Syntax(start));
            }
            while let Some(c) = self.peek() {
                if c.is_ascii_digit() {
                    self.pos += 1;
                } else {
                    break;
                }
            }
            let s = self.src[start..self.pos].to_str_lossy();
            return s.parse::<i64>().map_err(|_| ArithError::Syntax(start));
        }
        // Variable reference: optional `$`, then identifier.
        if self.peek() == Some(b'$') {
            self.pos += 1;
        }
        if let Some(c) = self.peek()
            && (c.is_ascii_alphabetic() || c == b'_')
        {
            let start = self.pos;
            while let Some(c) = self.peek() {
                if c.is_ascii_alphanumeric() || c == b'_' {
                    self.pos += 1;
                } else {
                    break;
                }
            }
            let name = &self.src[start..self.pos];
            let raw = self.env.get(name).cloned().unwrap_or_default();
            // Bash treats unset/non-numeric variables as 0.
            let s = raw.to_str_lossy();
            let trimmed = s.trim();
            if trimmed.is_empty() {
                return Ok(0);
            }
            // Recurse so a variable holding an expression evaluates.
            return eval(trimmed.as_bytes(), self.env);
        }
        Err(ArithError::Syntax(self.pos))
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn ev(s: &str) -> i64 {
        eval(s.as_bytes(), &Env::new()).unwrap()
    }

    #[test]
    fn literals() {
        assert_eq!(ev("0"), 0);
        assert_eq!(ev("42"), 42);
        assert_eq!(ev("0x1f"), 31);
    }

    #[test]
    fn addsub_muldiv() {
        assert_eq!(ev("1+2*3"), 7);
        assert_eq!(ev("(1+2)*3"), 9);
        assert_eq!(ev("10/3"), 3);
        assert_eq!(ev("10%3"), 1);
    }

    #[test]
    fn comparisons() {
        assert_eq!(ev("1 < 2"), 1);
        assert_eq!(ev("1 == 1"), 1);
        assert_eq!(ev("1 != 1"), 0);
    }

    #[test]
    fn logic() {
        assert_eq!(ev("1 && 0"), 0);
        assert_eq!(ev("0 || 5"), 1);
        assert_eq!(ev("!0"), 1);
    }

    #[test]
    fn ternary_and_neg() {
        assert_eq!(ev("1 ? 7 : 9"), 7);
        assert_eq!(ev("0 ? 7 : 9"), 9);
        assert_eq!(ev("-5 + 3"), -2);
    }

    #[test]
    fn variable_lookup() {
        let mut env = Env::new();
        env.set("X".into(), "10".into(), false, false);
        assert_eq!(eval(b"X * 2", &env).unwrap(), 20);
        assert_eq!(eval(b"$X + 5", &env).unwrap(), 15);
        assert_eq!(eval(b"UNSET + 1", &env).unwrap(), 1);
    }
}
